package governance

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// insightWindowDefault and insightWindowMax bound the "window" query
// parameter: default 7 days, reject anything over 30 days.
const (
	insightWindowDefault = 7 * 24 * time.Hour
	insightWindowMax     = 30 * 24 * time.Hour
)

// insightLimitDefault and insightLimitMax bound the "limit" query parameter,
// which caps how many attention items a response returns.
const (
	insightLimitDefault = 200
	insightLimitMax     = 200
)

// InsightHandlerConfig configures both the actor-scoped and admin governance
// insight endpoints. Session, audit, policy-decision, and kill-switch access
// go through Service (same package, so its unexported stores are reachable
// directly); Registry is passed explicitly because it is not part of
// SessionService, matching the RegistryHandler/AdminRegistryHandler pattern.
type InsightHandlerConfig struct {
	Service  *SessionService
	Registry RegistryStoreInterface
	// Now overrides the clock for deterministic tests. Nil means time.Now().
	Now func() time.Time
	// StalledAfter overrides the stalled-session threshold. Zero uses the
	// BuildAttentionQueue default (30 minutes).
	StalledAfter time.Duration
}

type insightHandler struct {
	cfg   InsightHandlerConfig
	admin bool
}

// NewInsightHandler serves GET /v1/reporting/governance-insights: the
// deterministic attention queue scoped to the requesting actor's own
// sessions, exactly like existing evidence/session listing scoping.
func NewInsightHandler(cfg InsightHandlerConfig) http.Handler {
	return &insightHandler{cfg: cfg, admin: false}
}

// NewAdminInsightHandler serves GET /v1/admin/reporting/governance-insights:
// the same projection, org-wide, gated by RequireAdminRequest.
func NewAdminInsightHandler(cfg InsightHandlerConfig) http.Handler {
	return &insightHandler{cfg: cfg, admin: true}
}

func (h *insightHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.cfg.Service == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "insight projection unavailable"})
		return
	}
	if h.cfg.Service.sessions == nil || h.cfg.Service.auditReader == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session or audit store unavailable"})
		return
	}

	var actor string
	if h.admin {
		if !h.cfg.Service.RequireAdminRequest(w, r) {
			return
		}
		actor = AdminOperatorSubject
	} else {
		authReq, ok := h.cfg.Service.RequireAuthorizedRequest(w, r)
		if !ok {
			return
		}
		r = authReq
		actor = actorFromContext(r.Context())
	}

	now := time.Now().UTC()
	if h.cfg.Now != nil {
		now = h.cfg.Now().UTC()
	}
	since, err := parseInsightWindow(r.URL.Query().Get("window"), now)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	limit := parseInsightLimit(r.URL.Query().Get("limit"))

	sessions, err := h.loadSessions(r.Context(), actor, since)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	sessionIDs := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		sessionIDs[session.SessionID] = struct{}{}
	}

	eventsBySession := make(map[string][]audit.Event, len(sessions))
	usageBySession := make(map[string]SessionUsageSummary, len(sessions))
	for _, session := range sessions {
		events, err := h.cfg.Service.auditReader.EventsBySession(r.Context(), session.SessionID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit lookup failed"})
			return
		}
		eventsBySession[session.SessionID] = events
		usageBySession[session.SessionID] = SummarizeSessionUsageWithPricing(r.Context(), events, h.cfg.Service.modelPricing)
	}

	var evidence []EvidenceRecord
	if h.cfg.Registry != nil {
		all, err := h.cfg.Registry.Evidence()
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "evidence lookup failed"})
			return
		}
		for _, record := range all {
			if _, ok := sessionIDs[record.SessionID]; ok {
				evidence = append(evidence, record)
			}
		}
	}

	var policyDecisions []PolicyDecisionRecord
	if h.cfg.Service.policyDecisions != nil {
		all, err := h.cfg.Service.policyDecisions.ListPolicyDecisions(r.Context(), "")
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "policy decision lookup failed"})
			return
		}
		for _, decision := range all {
			if decision.RecordedAt.Before(since) {
				continue
			}
			if !h.admin && decision.Actor != actor {
				continue
			}
			policyDecisions = append(policyDecisions, decision)
		}
	}

	var killSwitches map[string][]string
	if h.cfg.Service.killSwitchStore != nil {
		killSwitches = h.cfg.Service.killSwitchStore.List()
		if !h.admin {
			killSwitches = map[string][]string{"global": killSwitches["global"]}
		}
	}

	result := BuildAttentionQueue(InsightInputs{
		Now:             now,
		WindowSince:     since,
		Sessions:        sessions,
		EventsBySession: eventsBySession,
		UsageBySession:  usageBySession,
		Evidence:        evidence,
		PolicyDecisions: policyDecisions,
		KillSwitches:    killSwitches,
		StalledAfter:    h.cfg.StalledAfter,
	})
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
	}

	httpx.WriteJSON(w, http.StatusOK, result)
}

// loadSessions gathers the bounded, windowed session set the projection
// folds over: an actor's own sessions for the actor-scoped endpoint, every
// actor's sessions for the admin endpoint. Both are capped at
// insightSessionCap newest sessions regardless of the response item limit.
func (h *insightHandler) loadSessions(ctx context.Context, actor string, since time.Time) ([]SessionRecord, error) {
	if h.admin {
		lister, ok := h.cfg.Service.sessions.(sessionExportLister)
		if !ok {
			return nil, fmt.Errorf("session store does not support windowed export")
		}
		return lister.ListAllSince(ctx, since, insightSessionCap)
	}
	lister, ok := h.cfg.Service.sessions.(sessionWindowLister)
	if !ok {
		return nil, fmt.Errorf("session store does not support windowed listing")
	}
	return lister.ListRecentSince(ctx, actor, since, insightSessionCap)
}

// sessionWindowLister is satisfied by SQLiteSessionStore's ListRecentSince,
// discovered via type assertion the same way admin_sessions.go discovers
// ListRecentAll/ListAllSince.
type sessionWindowLister interface {
	ListRecentSince(ctx context.Context, actorSubject string, since time.Time, limit int) ([]SessionRecord, error)
}

// parseInsightWindow parses the "window" query parameter (24h, 7d, 30d
// style), defaulting to 7 days and rejecting anything over 30 days.
func parseInsightWindow(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return now.Add(-insightWindowDefault), nil
	}
	dur, err := parseWindowDuration(raw)
	if err != nil || dur <= 0 {
		return time.Time{}, fmt.Errorf("invalid window: %s", raw)
	}
	if dur > insightWindowMax {
		return time.Time{}, fmt.Errorf("window must not exceed 30d")
	}
	return now.Add(-dur), nil
}

// parseWindowDuration accepts Go's normal duration units (24h, 90m, ...) plus
// a day suffix (7d, 30d) for the common reporting-window shorthand.
func parseWindowDuration(raw string) (time.Duration, error) {
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid window: %s", raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(raw)
}

func parseInsightLimit(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return insightLimitDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return insightLimitDefault
	}
	if n > insightLimitMax {
		return insightLimitMax
	}
	return n
}
