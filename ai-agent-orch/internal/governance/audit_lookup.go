package governance

import (
	"context"
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
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

// AuditVerifyResponse reports the tamper-evidence check for a session's
// audit chain.
type AuditVerifyResponse struct {
	SessionID  string `json:"session_id"`
	EventCount int    `json:"event_count"`
	ChainValid bool   `json:"chain_valid"`
	Error      string `json:"error,omitempty"`
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
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if l == nil || l.audit == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "audit lookup unavailable"})
		return
	}
	authSubject := "local-dev"
	if l.authorizer != nil {
		subject, ok := l.authorizer.Validate(r.Context(), r.Header.Get("Authorization"))
		if !ok {
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		authSubject = subject
	} else if l.devToken == "" {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "dev token not configured"})
		return
	} else if !authorizedBearer(r.Header.Get("Authorization"), l.devToken) {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	} else if localIdentity := r.Header.Get("X-AI-Orch-Local-Identity"); localIdentity != "" {
		if !validActorLabel(localIdentity) {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid local identity"})
			return
		}
		authSubject = localIdentity
	}

	sessionID := strings.TrimPrefix(r.URL.Path, "/v1/audit/sessions/")
	verify := false
	if rest, ok := strings.CutSuffix(sessionID, "/verify"); ok {
		sessionID = rest
		verify = true
	}
	if sessionID == "" || strings.Contains(sessionID, "/") {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}
	if l.sessions != nil {
		record, err := l.sessions.Get(r.Context(), sessionID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return
		}
		if record.ActorSubject != authSubject {
			httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
			return
		}
	}
	events, err := l.audit.EventsBySession(r.Context(), sessionID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit lookup failed"})
		return
	}
	if verify {
		response := AuditVerifyResponse{
			SessionID:  sessionID,
			EventCount: len(events),
			ChainValid: true,
		}
		if err := audit.VerifyChain(events); err != nil {
			response.ChainValid = false
			response.Error = err.Error()
		}
		httpx.WriteJSON(w, http.StatusOK, response)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, AuditLookupResponse{
		SessionID:    sessionID,
		Events:       events,
		UsageSummary: SummarizeSessionUsageWithPricing(r.Context(), events, l.modelPricing),
	})
}
