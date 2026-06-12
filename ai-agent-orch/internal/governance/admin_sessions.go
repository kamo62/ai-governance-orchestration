package governance

import (
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// Admin oversight endpoints. Ordinary session and audit views are scoped to
// the requesting actor; governance operators need the cross-actor view, so
// these live under /v1/admin/ where the middleware enforces the admin token.

type sessionAllLister interface {
	ListRecentAll(ctx context.Context, limit int) ([]SessionRecord, error)
}

// NewAdminSessionsHandler serves GET /v1/admin/sessions: recent sessions
// across all actors.
func NewAdminSessionsHandler(service *SessionService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if service == nil || service.sessions == nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
			return
		}
		// Defense in depth: the middleware already admin-gates /v1/admin/*.
		if !service.RequireAdminRequest(w, r) {
			return
		}
		lister, ok := service.sessions.(sessionAllLister)
		if !ok {
			httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"error": "session store does not support cross-actor listing"})
			return
		}
		limit := parseSessionListLimit(r.URL.Query().Get("limit"))
		records, err := lister.ListRecentAll(r.Context(), limit)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session list failed"})
			return
		}
		summaries := make([]SessionSummary, 0, len(records))
		for _, record := range records {
			summary := sessionSummaryFromRecord(record)
			if service.auditReader != nil {
				events, err := service.auditReader.EventsBySession(r.Context(), record.SessionID)
				if err != nil {
					httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session usage lookup failed"})
					return
				}
				summary.UsageSummary = SummarizeSessionUsageWithPricing(r.Context(), events, service.modelPricing)
				applyLedgerFields(&summary, events)
			}
			summaries = append(summaries, summary)
		}
		httpx.WriteJSON(w, http.StatusOK, ListSessionsResponse{Sessions: summaries})
	})
}

// AdminAuditLookupConfig configures the cross-actor audit lookup.
type AdminAuditLookupConfig struct {
	Service      *SessionService
	Audit        AuditReader
	ModelPricing ModelPricingStore
}

// NewAdminAuditLookupHandler serves GET /v1/admin/audit/sessions/{id}: the
// session audit trail without actor-ownership scoping.
func NewAdminAuditLookupHandler(cfg AdminAuditLookupConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if cfg.Audit == nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "audit lookup unavailable"})
			return
		}
		if cfg.Service == nil || !cfg.Service.RequireAdminRequest(w, r) {
			return
		}
		sessionID := strings.TrimPrefix(r.URL.Path, "/v1/admin/audit/sessions/")
		if sessionID == "" || strings.Contains(sessionID, "/") {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
			return
		}
		events, err := cfg.Audit.EventsBySession(r.Context(), sessionID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit lookup failed"})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, AuditLookupResponse{
			SessionID:    sessionID,
			Events:       events,
			UsageSummary: SummarizeSessionUsageWithPricing(r.Context(), events, cfg.ModelPricing),
		})
	})
}

type sessionExportLister interface {
	ListAllSince(ctx context.Context, since time.Time, max int) ([]SessionRecord, error)
}

// NewAdminSessionsExportHandler serves GET /v1/admin/sessions/export: the full
// org session ledger as CSV (default) or JSON for compliance evidence.
// Optional `since` (RFC3339) bounds the range; results cap at 5000 rows.
func NewAdminSessionsExportHandler(service *SessionService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if service == nil || service.sessions == nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
			return
		}
		if !service.RequireAdminRequest(w, r) {
			return
		}
		lister, ok := service.sessions.(sessionExportLister)
		if !ok {
			httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"error": "session store does not support export"})
			return
		}
		var since time.Time
		if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "since must be RFC3339"})
				return
			}
			since = parsed
		}
		records, err := lister.ListAllSince(r.Context(), since, 5000)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session export failed"})
			return
		}
		summaries := make([]SessionSummary, 0, len(records))
		for _, record := range records {
			summary := sessionSummaryFromRecord(record)
			if service.auditReader != nil {
				if events, err := service.auditReader.EventsBySession(r.Context(), record.SessionID); err == nil {
					summary.UsageSummary = SummarizeSessionUsageWithPricing(r.Context(), events, service.modelPricing)
				}
			}
			summaries = append(summaries, summary)
		}
		stamp := time.Now().UTC().Format("20060102-150405")
		if strings.EqualFold(r.URL.Query().Get("format"), "json") {
			w.Header().Set("Content-Disposition", `attachment; filename="ai-orch-sessions-`+stamp+`.json"`)
			httpx.WriteJSON(w, http.StatusOK, ListSessionsResponse{Sessions: summaries})
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="ai-orch-sessions-`+stamp+`.csv"`)
		writer := csv.NewWriter(w)
		_ = writer.Write([]string{
			"session_id", "parent_session_id", "created_at", "actor", "agent", "routed_agent", "status",
			"classification", "work_item_id", "repo_url", "branch",
			"model_alias", "model_resolved", "gateway_backend",
			"prompt_tokens", "completion_tokens", "total_tokens",
			"estimated_cost_usd", "cost_source", "trust_level",
		})
		for _, s := range summaries {
			u := s.UsageSummary
			_ = writer.Write([]string{
				s.SessionID, s.ParentSessionID, s.CreatedAt.UTC().Format(time.RFC3339), s.ActorSubject, s.Agent, s.RoutedAgent, s.Status,
				s.Classification, s.WorkItemID, s.RepoURL, s.Branch,
				u.ModelAlias, u.ModelResolved, u.GatewayBackend,
				strconv.Itoa(u.PromptTokens), strconv.Itoa(u.CompletionTokens), strconv.Itoa(u.TotalTokens),
				strconv.FormatFloat(u.EstimatedCostUSD, 'f', -1, 64), u.CostSource, s.TrustLevel,
			})
		}
		writer.Flush()
	})
}
