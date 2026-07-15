package governance

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/copilot"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

type DeveloperHandlerConfig struct {
	DevToken             string
	Authorizer           RequestAuthorizer
	CopilotStore         *copilot.Store
	CredentialStore      DeveloperCredentialStore
	Now                  func() time.Time
	RuntimeCredentialTTL time.Duration
}

type DeveloperHandler struct {
	devToken        string
	authorizer      RequestAuthorizer
	copilotStore    *copilot.Store
	credentialStore DeveloperCredentialStore
	now             func() time.Time
	ttl             time.Duration
}

func NewDeveloperHandler(cfg DeveloperHandlerConfig) http.Handler {
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	ttl := cfg.RuntimeCredentialTTL
	if ttl <= 0 {
		ttl = defaultDeveloperRuntimeCredentialTTL
	}
	return &DeveloperHandler{
		devToken:        cfg.DevToken,
		authorizer:      cfg.Authorizer,
		copilotStore:    cfg.CopilotStore,
		credentialStore: cfg.CredentialStore,
		now:             now,
		ttl:             ttl,
	}
}

func (h *DeveloperHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/developer/runtime-credential":
		h.runtimeCredential(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *DeveloperHandler) runtimeCredential(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	if h.copilotStore == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "copilot token store unavailable; operator must configure server-side Copilot storage before developer runtime credentials can be issued"})
		return
	}
	if h.credentialStore == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "developer runtime credential store unavailable"})
		return
	}
	if _, err := h.copilotStore.Load(r.Context(), actor); err != nil {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "copilot enrollment required before issuing an ai-orch runtime credential"})
		return
	}
	var body struct {
		Client     string `json:"client"`
		DeviceName string `json:"device_name"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
	now := h.now().UTC()
	rec, token, err := h.credentialStore.Issue(r.Context(), DeveloperCredentialIssue{
		ActorSubject: actor,
		Client:       body.Client,
		DeviceName:   body.DeviceName,
		Now:          now,
		TTL:          h.ttl,
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	days := int(rec.ExpiresAt.Sub(rec.IssuedAt).Hours() / 24)
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"actor_subject":    rec.ActorSubject,
		"client":           rec.Client,
		"credential_id":    rec.ID,
		"device_name_hash": rec.DeviceNameHash,
		"runtime_token":    token,
		"token_type":       "Bearer",
		"issued_at":        rec.IssuedAt,
		"expires_at":       rec.ExpiresAt,
		"expires_in_days":  days,
	})
}

func (h *DeveloperHandler) authorized(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.authorizer != nil {
		subject, ok := h.authorizer.Validate(r.Context(), r.Header.Get("Authorization"))
		if !ok {
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return "", false
		}
		return subject, true
	}
	if h.devToken == "" || !authorizedBearer(r.Header.Get("Authorization"), h.devToken) {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return "", false
	}
	subject := "local-dev"
	if localIdentity := strings.TrimSpace(r.Header.Get("X-AI-Orch-Local-Identity")); localIdentity != "" && validActorLabel(localIdentity) {
		subject = localIdentity
	}
	return subject, true
}
