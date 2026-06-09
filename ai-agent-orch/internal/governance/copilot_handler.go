package governance

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"ai-agent-orch/internal/copilot"
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
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	_, actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	if h.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"configured": false, "error": "copilot token store unavailable"})
		return
	}
	rec, err := h.store.Load(r.Context(), actor)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "actor_subject": actor})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "actor_subject": actor, "github_login": rec.GitHubLogin, "token_fingerprint": rec.Fingerprint})
}

func (h *CopilotHandler) loginStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	_, actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	if h.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "copilot token store unavailable"})
		return
	}
	device, err := h.client.StartDeviceFlow(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	loginID := h.newID("copilot_login")
	h.mu.Lock()
	h.logins[loginID] = copilotLoginState{ActorSubject: actor, Device: device, StartedAt: time.Now().UTC()}
	h.mu.Unlock()
	go h.completeLogin(loginID)
	writeJSON(w, http.StatusAccepted, map[string]any{
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
			err = h.store.Save(ctx, copilot.TokenRecord{ActorSubject: state.ActorSubject, GitHubLogin: user.Login, GitHubUserID: fmt.Sprintf("%d", user.ID), BaseURL: copilot.DefaultCopilotBaseURL, AccessToken: token.AccessToken})
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

func (h *CopilotHandler) loginStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
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
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "login not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"login_id": loginID, "done": state.Done, "error": state.Error, "github_login": state.GitHubLogin})
}

func (h *CopilotHandler) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	_, actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	rec, err := h.store.Load(r.Context(), actor)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "copilot token missing"})
		return
	}
	body, err := h.client.Models(r.Context(), rec.AccessToken)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *CopilotHandler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	_, actor, ok := h.authorized(w, r)
	if !ok {
		return
	}
	if h.store != nil {
		_ = h.store.Delete(r.Context(), actor)
	}
	writeJSON(w, http.StatusOK, map[string]any{"actor_subject": actor, "configured": false})
}

func (h *CopilotHandler) authorized(w http.ResponseWriter, r *http.Request) (*http.Request, string, bool) {
	if h.authorizer != nil {
		subject, ok := h.authorizer.Validate(r.Context(), r.Header.Get("Authorization"))
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return r, "", false
		}
		return r, subject, true
	}
	if h.devToken == "" || !authorizedBearer(r.Header.Get("Authorization"), h.devToken) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return r, "", false
	}
	subject := "local-dev"
	if localIdentity := r.Header.Get("X-AI-Orch-Local-Identity"); localIdentity != "" && validActorLabel(localIdentity) {
		subject = localIdentity
	}
	return r, subject, true
}
