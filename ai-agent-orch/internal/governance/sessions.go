package governance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/policyengine"
)

type AuditAppender interface {
	Append(context.Context, audit.Event) (audit.Event, error)
}

// Auth context propagation.
type authContextKey string

const authInfoKey authContextKey = "authInfo"

// AuthInfo holds the authenticated subject and method.
type AuthInfo struct {
	Subject string
	Method  string
}

// WithAuthInfo injects auth info into a context.
func WithAuthInfo(ctx context.Context, info AuthInfo) context.Context {
	return context.WithValue(ctx, authInfoKey, info)
}

// AuthInfoFromContext extracts auth info from a context.
func AuthInfoFromContext(ctx context.Context) (AuthInfo, bool) {
	v, ok := ctx.Value(authInfoKey).(AuthInfo)
	return v, ok
}

type SessionConfig struct {
	DevToken          string
	Authorizer        RequestAuthorizer
	Audit             AuditAppender
	Sessions          SessionStore
	ClassificationMax string
	KillSwitch        bool
	KillSwitchStore   KillSwitchStore
	CostCapEnabled    bool
	SessionCostCapUSD float64
	PolicyEngine      policyengine.Engine
	ToolLoopMax       int
	PatchBuffer       *PatchBuffer
	Metrics           *MetricsHandler
	NewID             func(prefix string) string
}

type SessionService struct {
	devToken          string
	authorizer        RequestAuthorizer
	audit             AuditAppender
	sessions          SessionStore
	classificationMax string
	killSwitch        bool
	killSwitchStore   KillSwitchStore
	costCapEnabled    bool
	sessionCostCapUSD float64
	policyEngine      policyengine.Engine
	toolLoopMax       int
	patchBuffer       *PatchBuffer
	metrics           *MetricsHandler
	newID             func(prefix string) string
	promptMu          sync.Mutex
	prompts           map[string]string
	patchMu           sync.Mutex
	patches           map[string]map[string]struct{}
	cancelMu          sync.Mutex
	cancels           map[string]context.CancelFunc
}

func (s *SessionService) registerCancel(sessionID string, cancel context.CancelFunc) {
	if s == nil || sessionID == "" {
		return
	}
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.cancels == nil {
		s.cancels = make(map[string]context.CancelFunc)
	}
	s.cancels[sessionID] = cancel
}

func (s *SessionService) cancelExecution(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if cancel, ok := s.cancels[sessionID]; ok {
		cancel()
		delete(s.cancels, sessionID)
	}
}

func (s *SessionService) setSessionStatus(ctx context.Context, sessionID string, status string) {
	if s == nil || s.sessions == nil || sessionID == "" || status == "" {
		return
	}
	_ = s.sessions.UpdateStatus(ctx, sessionID, status)
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
	engine := cfg.PolicyEngine
	if engine == nil {
		engine, _ = policyengine.New("native")
	}
	patchBuffer := cfg.PatchBuffer
	if patchBuffer == nil {
		patchBuffer = NewPatchBuffer()
	}
	toolLoopMax := cfg.ToolLoopMax
	if toolLoopMax <= 0 {
		toolLoopMax = 15
	}
	return &SessionService{
		devToken:          cfg.DevToken,
		authorizer:        cfg.Authorizer,
		audit:             cfg.Audit,
		sessions:          cfg.Sessions,
		classificationMax: defaultString(cfg.ClassificationMax, "internal"),
		killSwitch:        cfg.KillSwitch,
		killSwitchStore:   cfg.KillSwitchStore,
		costCapEnabled:    cfg.CostCapEnabled,
		sessionCostCapUSD: cfg.SessionCostCapUSD,
		policyEngine:      engine,
		toolLoopMax:       toolLoopMax,
		patchBuffer:       patchBuffer,
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
	authReq, ok := s.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq
	if blocked, reason := s.blockedByKillSwitch(""); blocked {
		if err := s.appendDenied(r.Context(), reason, nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		writeJSON(w, http.StatusLocked, map[string]any{"error": reason})
		return
	}

	var request CreateSessionRequest
	if err := readJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
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
	findings := detectSecrets(request.Prompt)
	decision, err := s.evaluatePolicy(r.Context(), policyengine.Request{
		AgentName:      request.Agent,
		ActionType:     "session.create",
		Classification: request.Classification,
		Findings:       findings,
		Metadata: map[string]any{
			"prompt_length": len(request.Prompt),
		},
	})
	if err != nil {
		if auditErr := s.appendDenied(r.Context(), "policy engine unavailable", nil, request.Classification); auditErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "policy engine unavailable"})
		return
	}
	if !decision.Allowed {
		reason := decision.Reason
		if reason == "" {
			reason = "policy denied"
		}
		if len(findings) > 0 {
			reason = "secret detected"
		}
		if err := s.appendDenied(r.Context(), reason, findings, request.Classification); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		if len(findings) > 0 {
			s.recordSecretBlocked()
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "secret detected"})
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": reason})
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

	authInfo, _ := AuthInfoFromContext(r.Context())
	actor := authInfo.Subject
	if actor == "" {
		actor = "local-dev"
	}

	event, err := s.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		SessionID:          sessionID,
		EventType:          "session.created",
		Actor:              actor,
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

	// Persist only after the authoritative audit event is durable.
	if s.sessions != nil {
		if err := s.sessions.Create(r.Context(), SessionRecord{
			SessionID:      sessionID,
			ActorSubject:   actor,
			Agent:          request.Agent,
			Classification: request.Classification,
			PromptSHA256:   hex.EncodeToString(promptHash[:]),
			Status:         "created",
			CreatedAt:      time.Now().UTC(),
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "session store failed"})
			return
		}
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

// actorFromContext extracts the authenticated subject from the context, falling back to "local-dev".
func actorFromContext(ctx context.Context) string {
	if info, ok := AuthInfoFromContext(ctx); ok && info.Subject != "" {
		return info.Subject
	}
	return "local-dev"
}

func (s *SessionService) appendDeniedWithCost(ctx context.Context, reason string, classification string, estimatedCostUSD float64, costCapUSD float64) error {
	if s == nil || s.audit == nil {
		return nil
	}
	_, err := s.audit.Append(ctx, audit.Event{
		EventID:            s.newID("evt"),
		EventType:          "session.denied",
		Actor:              actorFromContext(ctx),
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
		Actor:              actorFromContext(ctx),
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

func (s *SessionService) evaluatePolicy(ctx context.Context, req policyengine.Request) (policyengine.Decision, error) {
	if s == nil || s.policyEngine == nil {
		return policyengine.Decision{Allowed: true, Reason: "allowed", Engine: "native"}, nil
	}
	if req.UserID == "" {
		req.UserID = actorFromContext(ctx)
	}
	return s.policyEngine.Evaluate(ctx, req)
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

// RequireAuthorizedRequest validates the request and injects auth info into the request context.
// Callers must use the returned *http.Request to access the authenticated subject.
func (s *SessionService) RequireAuthorizedRequest(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if s == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return r, false
	}
	if s.authorizer != nil {
		subject, ok := s.authorizer.Validate(r.Context(), r.Header.Get("Authorization"))
		if ok {
			r = r.WithContext(WithAuthInfo(r.Context(), AuthInfo{Subject: subject, Method: "oidc"}))
			return r, true
		}
		if err := s.appendDenied(r.Context(), "invalid bearer token", nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return r, false
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return r, false
	}
	if s.devToken == "" {
		if err := s.appendDenied(r.Context(), "dev token not configured", nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return r, false
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "dev token not configured"})
		return r, false
	}
	if !s.authorized(r.Header.Get("Authorization")) {
		if err := s.appendDenied(r.Context(), "invalid dev token", nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return r, false
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return r, false
	}
	r = r.WithContext(WithAuthInfo(r.Context(), AuthInfo{Subject: "local-dev", Method: "dev"}))
	return r, true
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
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, prefix)), []byte(token)) == 1
}

// maxRequestBodyBytes limits JSON request bodies to 1 MiB.
const maxRequestBodyBytes = 1 << 20

// readJSON decodes a JSON request body with size limits and unknown field rejection.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
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
