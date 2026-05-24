package governance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"ai-agent-orch/internal/audit"
)

type AuditAppender interface {
	Append(context.Context, audit.Event) (audit.Event, error)
}

type SessionConfig struct {
	DevToken          string
	Authorizer        RequestAuthorizer
	Audit             AuditAppender
	ClassificationMax string
	KillSwitch        bool
	KillSwitchStore   KillSwitchStore
	CostCapEnabled    bool
	SessionCostCapUSD float64
	Metrics           *MetricsHandler
	NewID             func(prefix string) string
}

type SessionService struct {
	devToken          string
	authorizer        RequestAuthorizer
	audit             AuditAppender
	classificationMax string
	killSwitch        bool
	killSwitchStore   KillSwitchStore
	costCapEnabled    bool
	sessionCostCapUSD float64
	metrics           *MetricsHandler
	newID             func(prefix string) string
	promptMu          sync.Mutex
	prompts           map[string]string
	patchMu           sync.Mutex
	patches           map[string]map[string]struct{}
}

type CreateSessionRequest struct {
	Agent            string  `json:"agent"`
	Classification   string  `json:"classification"`
	Prompt           string  `json:"prompt"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd,omitempty"`
}

type CreateSessionResponse struct {
	SessionID    string `json:"session_id"`
	Status       string `json:"status"`
	Agent        string `json:"agent"`
	AuditEventID string `json:"audit_event_id"`
}

func NewSessionService(cfg SessionConfig) *SessionService {
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	return &SessionService{
		devToken:          cfg.DevToken,
		authorizer:        cfg.Authorizer,
		audit:             cfg.Audit,
		classificationMax: defaultString(cfg.ClassificationMax, "internal"),
		killSwitch:        cfg.KillSwitch,
		killSwitchStore:   cfg.KillSwitchStore,
		costCapEnabled:    cfg.CostCapEnabled,
		sessionCostCapUSD: cfg.SessionCostCapUSD,
		metrics:           cfg.Metrics,
		newID:             newID,
		prompts:           make(map[string]string),
		patches:           make(map[string]map[string]struct{}),
	}
}

func NewSessionHandler(service *SessionService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", service.createSession)
	return mux
}

func (s *SessionService) createSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if s == nil || s.audit == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	if !s.RequireAuthorizedRequest(w, r) {
		return
	}
	if blocked, reason := s.blockedByKillSwitch(""); blocked {
		if err := s.appendDenied(r.Context(), reason, nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		writeJSON(w, http.StatusLocked, map[string]any{"error": reason})
		return
	}

	var request CreateSessionRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if err := request.validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if blocked, reason := s.blockedByKillSwitch(request.Agent); blocked {
		if err := s.appendDenied(r.Context(), reason, nil, request.Classification); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		writeJSON(w, http.StatusLocked, map[string]any{"error": reason})
		return
	}
	exceeds, err := classificationExceedsMax(request.Classification, s.classificationMax)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if exceeds {
		reason := fmt.Sprintf("classification %s exceeds max %s", request.Classification, s.classificationMax)
		if err := s.appendDenied(r.Context(), reason, nil, request.Classification); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		s.recordClassificationBlocked()
		writeJSON(w, http.StatusForbidden, map[string]any{"error": reason})
		return
	}
	if findings := detectSecrets(request.Prompt); len(findings) > 0 {
		if err := s.appendDenied(r.Context(), "secret detected", findings, request.Classification); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		s.recordSecretBlocked()
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "secret detected"})
		return
	}
	if s.costCapEnabled && s.sessionCostCapUSD > 0 && request.EstimatedCostUSD > s.sessionCostCapUSD {
		if err := s.appendDeniedWithCost(r.Context(), "cost cap exceeded", request.Classification, request.EstimatedCostUSD, s.sessionCostCapUSD); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		s.recordCostCapped()
		writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": "cost cap exceeded"})
		return
	}

	sessionID := s.newID("sess")
	eventID := s.newID("evt")
	promptHash := sha256.Sum256([]byte(request.Prompt))
	event, err := s.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		SessionID:          sessionID,
		EventType:          "session.created",
		Actor:              "local-dev",
		Agent:              request.Agent,
		Classification:     request.Classification,
		PromptSHA256:       hex.EncodeToString(promptHash[:]),
		EstimatedCostUSD:   request.EstimatedCostUSD,
		CostCapUSD:         activeCostCap(s.costCapEnabled, s.sessionCostCapUSD),
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}
	s.recordSessionCreated()
	s.rememberPrompt(sessionID, request.Prompt)

	writeJSON(w, http.StatusCreated, CreateSessionResponse{
		SessionID:    sessionID,
		Status:       "created",
		Agent:        request.Agent,
		AuditEventID: event.EventID,
	})
}

func (s *SessionService) appendDeniedWithCost(ctx context.Context, reason string, classification string, estimatedCostUSD float64, costCapUSD float64) error {
	if s == nil || s.audit == nil {
		return nil
	}
	_, err := s.audit.Append(ctx, audit.Event{
		EventID:            s.newID("evt"),
		EventType:          "session.denied",
		Actor:              "local-dev",
		Classification:     classification,
		Reason:             reason,
		EstimatedCostUSD:   estimatedCostUSD,
		CostCapUSD:         costCapUSD,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
	})
	if err == nil {
		s.recordSessionDenied()
	}
	return err
}

func (s *SessionService) appendDenied(ctx context.Context, reason string, findings []string, classification string) error {
	if s == nil || s.audit == nil {
		return nil
	}
	_, err := s.audit.Append(ctx, audit.Event{
		EventID:            s.newID("evt"),
		EventType:          "session.denied",
		Actor:              "local-dev",
		Classification:     classification,
		Reason:             reason,
		Findings:           findings,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
	})
	if err == nil {
		s.recordSessionDenied()
	}
	return err
}

func (s *SessionService) recordSessionCreated() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordSessionCreated()
	}
}

func (s *SessionService) recordSessionDenied() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordSessionDenied()
	}
}

func (s *SessionService) recordSecretBlocked() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordSecretBlocked()
	}
}

func (s *SessionService) recordClassificationBlocked() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordClassificationBlocked()
	}
}

func (s *SessionService) recordCostCapped() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordCostCapped()
	}
}

func (s *SessionService) authorized(header string) bool {
	return authorizedBearer(header, s.devToken)
}

func (s *SessionService) RequireAuthorizedRequest(w http.ResponseWriter, r *http.Request) bool {
	if s == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return false
	}
	if s.authorizer != nil {
		if _, ok := s.authorizer.Validate(r.Context(), r.Header.Get("Authorization")); ok {
			return true
		}
		if err := s.appendDenied(r.Context(), "invalid bearer token", nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return false
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return false
	}
	if s.devToken == "" {
		if err := s.appendDenied(r.Context(), "dev token not configured", nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return false
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "dev token not configured"})
		return false
	}
	if !s.authorized(r.Header.Get("Authorization")) {
		if err := s.appendDenied(r.Context(), "invalid dev token", nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return false
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return false
	}
	return true
}

type RequestAuthorizer interface {
	Validate(ctx context.Context, header string) (subject string, ok bool)
}

func (s *SessionService) blockedByKillSwitch(agent string) (bool, string) {
	if s == nil {
		return false, ""
	}
	if s.killSwitch {
		return true, "kill switch enabled"
	}
	if s.killSwitchStore == nil {
		return false, ""
	}
	if s.killSwitchStore.IsBlocked("global", "all") || s.killSwitchStore.IsBlocked("global", "sessions") {
		return true, "kill switch enabled"
	}
	if agent != "" && s.killSwitchStore.IsBlocked("agent", agent) {
		return true, fmt.Sprintf("kill switch enabled for agent %s", agent)
	}
	return false, ""
}

func (s *SessionService) rememberPrompt(sessionID string, prompt string) {
	if s == nil || sessionID == "" || prompt == "" {
		return
	}
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	s.prompts[sessionID] = prompt
}

func (s *SessionService) promptForSession(sessionID string) (string, bool) {
	if s == nil || sessionID == "" {
		return "", false
	}
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	prompt, ok := s.prompts[sessionID]
	return prompt, ok
}

func (s *SessionService) forgetPrompt(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	delete(s.prompts, sessionID)
}

func (s *SessionService) rememberPatch(sessionID string, patchID string) {
	if s == nil || sessionID == "" || patchID == "" {
		return
	}
	s.patchMu.Lock()
	defer s.patchMu.Unlock()
	if s.patches[sessionID] == nil {
		s.patches[sessionID] = make(map[string]struct{})
	}
	s.patches[sessionID][patchID] = struct{}{}
}

func (s *SessionService) patchKnown(sessionID string, patchID string) bool {
	if s == nil || sessionID == "" || patchID == "" {
		return false
	}
	s.patchMu.Lock()
	defer s.patchMu.Unlock()
	patches := s.patches[sessionID]
	_, ok := patches[patchID]
	return ok
}

func authorizedBearer(header string, token string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return strings.TrimPrefix(header, prefix) == token
}

func (r CreateSessionRequest) validate() error {
	if r.Agent == "" {
		return errors.New("agent is required")
	}
	if r.Classification == "" {
		return errors.New("classification is required")
	}
	if r.Prompt == "" {
		return errors.New("prompt is required")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func randomID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_fallback", prefix)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func defaultString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func defaultFloat(value float64, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	return value
}

func activeCostCap(enabled bool, value float64) float64 {
	if !enabled {
		return 0
	}
	return value
}
