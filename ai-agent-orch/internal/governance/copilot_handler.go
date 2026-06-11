package governance

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/copilot"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

type CopilotHandlerConfig struct {
	DevToken   string
	Authorizer RequestAuthorizer
	Store      *copilot.Store
	Client     *copilot.Client
	NewID      func(prefix string) string
}

type CopilotHandler struct {
	devToken   string
	authorizer RequestAuthorizer
	store      *copilot.Store
	client     *copilot.Client
	newID      func(prefix string) string
	mu         sync.Mutex
	logins     map[string]copilotLoginState
}

type copilotLoginState struct {
	ActorSubject string
	Device       copilot.DeviceCodeResponse
	StartedAt    time.Time
	Done         bool
	Error        string
	GitHubLogin  string
}

func NewCopilotHandler(cfg CopilotHandlerConfig) http.Handler {
	client := cfg.Client
	if client == nil {
		client = copilot.NewClient()
	}
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	h := &CopilotHandler{
		devToken:   cfg.DevToken,
		authorizer: cfg.Authorizer,
		store:      cfg.Store,
		client:     client,
		newID:      newID,
		logins:     make(map[string]copilotLoginState),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/copilot/status", h.status)
	mux.HandleFunc("/v1/copilot/login/start", h.loginStart)
	mux.HandleFunc("/v1/copilot/login/", h.loginStatus)
	mux.HandleFunc("/v1/copilot/models", h.models)
	mux.HandleFunc("/v1/copilot/logout", h.logout)
	return mux
}

func (h *CopilotHandler) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	_, actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	if h.store == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": false, "error": "copilot token store unavailable"})
		return
	}
	// Fleet-level enrollment count for operator dashboards: enrollment itself
	// stays developer self-service, the console only observes adoption.
	enrollments := 0
	if count, countErr := h.store.EnrollmentCount(r.Context()); countErr == nil {
		enrollments = count
	}
	rec, err := h.store.Load(r.Context(), actor)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"configured": false, "actor_subject": actor, "enrollments": enrollments})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"configured": true, "actor_subject": actor, "github_login": rec.GitHubLogin, "token_fingerprint": rec.Fingerprint, "refresh_configured": rec.RefreshToken != "", "access_expires_at": rec.AccessExpiresAt, "enrollments": enrollments})
}

func (h *CopilotHandler) loginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	_, actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	if h.store == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "copilot token store unavailable"})
		return
	}
	device, err := h.client.StartDeviceFlow(r.Context())
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	loginID := h.newID("copilot_login")
	h.mu.Lock()
	h.pruneLoginsLocked()
	h.logins[loginID] = copilotLoginState{ActorSubject: actor, Device: device, StartedAt: time.Now().UTC()}
	h.mu.Unlock()
	go h.completeLogin(loginID)
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"login_id":                  loginID,
		"actor_subject":             actor,
		"user_code":                 device.UserCode,
		"verification_uri":          device.VerificationURI,
		"verification_uri_complete": device.VerificationURIComplete,
		"expires_in":                device.ExpiresIn,
		"interval":                  device.Interval,
	})
}

func (h *CopilotHandler) completeLogin(loginID string) {
	h.mu.Lock()
	state := h.logins[loginID]
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(state.Device.ExpiresIn+60)*time.Second)
	defer cancel()
	token, err := h.client.PollAccessToken(ctx, state.Device)
	if err == nil {
		user, userErr := h.client.User(ctx, token.AccessToken)
		if userErr != nil {
			err = userErr
		} else {
			now := time.Now().UTC()
			err = h.store.Save(ctx, copilot.TokenRecord{
				ActorSubject:     state.ActorSubject,
				GitHubLogin:      user.Login,
				GitHubUserID:     fmt.Sprintf("%d", user.ID),
				BaseURL:          copilot.DefaultCopilotBaseURL,
				AccessToken:      token.AccessToken,
				RefreshToken:     token.RefreshToken,
				AccessExpiresAt:  token.AccessExpiresAt(now),
				RefreshExpiresAt: token.RefreshExpiresAt(now),
			})
			state.GitHubLogin = user.Login
		}
	}
	h.mu.Lock()
	state.Done = err == nil
	if err != nil {
		state.Error = err.Error()
	}
	h.logins[loginID] = state
	h.mu.Unlock()
}

// pruneLoginsLocked drops device-flow attempts past any plausible authorization
// window so abandoned logins do not accumulate. Callers must hold h.mu.
func (h *CopilotHandler) pruneLoginsLocked() {
	cutoff := time.Now().UTC().Add(-1 * time.Hour)
	for id, state := range h.logins {
		expiry := state.StartedAt.Add(time.Duration(state.Device.ExpiresIn+300) * time.Second)
		if expiry.Before(time.Now().UTC()) || state.StartedAt.Before(cutoff) {
			delete(h.logins, id)
		}
	}
}

func (h *CopilotHandler) loginStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	_, actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	loginID := strings.TrimPrefix(r.URL.Path, "/v1/copilot/login/")
	h.mu.Lock()
	state, exists := h.logins[loginID]
	h.mu.Unlock()
	if !exists || state.ActorSubject != actor {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "login not found"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"login_id": loginID, "done": state.Done, "error": state.Error, "github_login": state.GitHubLogin})
}

func (h *CopilotHandler) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	_, actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	rec, err := h.store.Load(r.Context(), actor)
	if err != nil {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "copilot token missing"})
		return
	}
	body, err := h.client.Models(r.Context(), h.copilotBearer(r.Context(), rec))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *CopilotHandler) copilotBearer(ctx context.Context, rec copilot.TokenRecord) string {
	if h.store != nil && rec.RefreshToken != "" && !rec.AccessExpiresAt.IsZero() && time.Now().UTC().After(rec.AccessExpiresAt.Add(-5*time.Minute)) {
		refreshed, err := h.client.RefreshAccessToken(ctx, rec.RefreshToken)
		if err == nil {
			if updated, updateErr := h.store.UpdateOAuthToken(ctx, rec.ActorSubject, refreshed, time.Now().UTC()); updateErr == nil {
				rec = updated
			}
		}
	}
	session, err := h.client.ExchangeSessionToken(ctx, rec.AccessToken)
	if err != nil {
		return rec.AccessToken
	}
	return session.Token
}

func (h *CopilotHandler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	_, actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	if h.store != nil {
		_ = h.store.Delete(r.Context(), actor)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"actor_subject": actor, "configured": false})
}

func (h *CopilotHandler) authorized(w http.ResponseWriter, r *http.Request) (*http.Request, string, bool) {
	if h.authorizer != nil {
		subject, ok := h.authorizer.Validate(r.Context(), r.Header.Get("Authorization"))
		if !ok {
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return r, "", false
		}
		return r, subject, true
	}
	if h.devToken == "" || !authorizedBearer(r.Header.Get("Authorization"), h.devToken) {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return r, "", false
	}
	subject := "local-dev"
	if localIdentity := r.Header.Get("X-AI-Orch-Local-Identity"); localIdentity != "" && validActorLabel(localIdentity) {
		subject = localIdentity
	}
	return r, subject, true
}
