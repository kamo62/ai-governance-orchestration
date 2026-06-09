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
	Sessions     SessionStore
}

type AuditLookup struct {
	devToken     string
	authorizer   RequestAuthorizer
	audit        AuditReader
	modelPricing ModelPricingStore
	sessions     SessionStore
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
		sessions:     cfg.Sessions,
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
	authSubject := "local-dev"
	if l.authorizer != nil {
		subject, ok := l.authorizer.Validate(r.Context(), r.Header.Get("Authorization"))
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		authSubject = subject
	} else if l.devToken == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "dev token not configured"})
		return
	} else if !authorizedBearer(r.Header.Get("Authorization"), l.devToken) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	} else if localIdentity := r.Header.Get("X-AI-Orch-Local-Identity"); localIdentity != "" {
		if !validActorLabel(localIdentity) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid local identity"})
			return
		}
		authSubject = localIdentity
	}

	sessionID := strings.TrimPrefix(r.URL.Path, "/v1/audit/sessions/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}
	if l.sessions != nil {
		record, err := l.sessions.Get(r.Context(), sessionID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return
		}
		if record.ActorSubject != authSubject {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
			return
		}
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
