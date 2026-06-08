package governance

import (
	"context"
	"net/http"
	"strings"

	"ai-agent-orch/internal/audit"
)

type AuditReader interface {
	EventsBySession(context.Context, string) ([]audit.Event, error)
}

type AuditLookupConfig struct {
	DevToken     string
	Authorizer   RequestAuthorizer
	Audit        AuditReader
	ModelPricing ModelPricingStore
}

type AuditLookup struct {
	devToken     string
	authorizer   RequestAuthorizer
	audit        AuditReader
	modelPricing ModelPricingStore
}

type AuditLookupResponse struct {
	SessionID    string              `json:"session_id"`
	Events       []audit.Event       `json:"events"`
	UsageSummary SessionUsageSummary `json:"usage_summary"`
}

func NewAuditLookupHandler(cfg AuditLookupConfig) http.Handler {
	lookup := &AuditLookup{
		devToken:     cfg.DevToken,
		authorizer:   cfg.Authorizer,
		audit:        cfg.Audit,
		modelPricing: cfg.ModelPricing,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audit/sessions/", lookup.lookupSession)
	return mux
}

func (l *AuditLookup) lookupSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if l == nil || l.audit == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "audit lookup unavailable"})
		return
	}
	if l.authorizer != nil {
		if _, ok := l.authorizer.Validate(r.Context(), r.Header.Get("Authorization")); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
	} else if l.devToken == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "dev token not configured"})
		return
	} else if !authorizedBearer(r.Header.Get("Authorization"), l.devToken) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	sessionID := strings.TrimPrefix(r.URL.Path, "/v1/audit/sessions/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}
	events, err := l.audit.EventsBySession(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit lookup failed"})
		return
	}
	writeJSON(w, http.StatusOK, AuditLookupResponse{
		SessionID:    sessionID,
		Events:       events,
		UsageSummary: SummarizeSessionUsageWithPricing(r.Context(), events, l.modelPricing),
	})
}
