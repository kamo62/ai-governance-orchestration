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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpauth"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
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
	AdminToken        string // Separate admin token for /v1/admin/* endpoints.
	Authorizer        RequestAuthorizer
	Audit             AuditAppender
	AuditReader       AuditReader
	Sessions          SessionStore
	ModelPricing      ModelPricingStore
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
	LocalStateTTL     time.Duration
	RequireWorkItem   bool
	// ContextResolver auto-resolves git/repo/branch metadata when not provided explicitly.
	ContextResolver ContextResolver
	// TrustedClientToken, when set, gates the privileged trust levels
	// (gateway_enforced, managed_client). A caller may only raise the recorded
	// trust level above self_reported by proving possession of this shared
	// secret via the X-AI-Orch-Trusted-Client-Token header. When empty (local
	// dev), the X-AI-Orch-Client identity header is honored on its own.
	TrustedClientToken string
}

// ContextResolver extracts session context from the local environment.
type ContextResolver interface {
	Resolve() SessionContext
}

// SessionContext holds non-authoritative metadata resolved from the local environment.
type SessionContext struct {
	RepoURL      string `json:"repo_url,omitempty"`
	Branch       string `json:"branch,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	WorkItemID   string `json:"work_item_id,omitempty"`
	WorkItemType string `json:"work_item_type,omitempty"`
	ActorHint    string `json:"actor_hint,omitempty"`
	SourceSystem string `json:"source_system,omitempty"`
}

// defaultLocalStateTTL is the time-to-live for abandoned in-process state
// (prompts, patches, cancellations) before lazy eviction occurs.
const defaultLocalStateTTL = 30 * time.Minute

type SessionService struct {
	devToken           string
	adminToken         string // Separate token for admin endpoints; empty means admin is disabled.
	authorizer         RequestAuthorizer
	audit              AuditAppender
	auditReader        AuditReader
	sessions           SessionStore
	modelPricing       ModelPricingStore
	classificationMax  string
	killSwitch         bool
	killSwitchStore    KillSwitchStore
	costCapEnabled     bool
	sessionCostCapUSD  float64
	policyEngine       policyengine.Engine
	toolLoopMax        int
	patchBuffer        *PatchBuffer
	metrics            *MetricsHandler
	newID              func(prefix string) string
	contextResolver    ContextResolver
	trustedClientToken string
	promptMu           sync.Mutex
	prompts            map[string]string
	promptTimes        map[string]time.Time
	patchMu            sync.Mutex
	patches            map[string]map[string]struct{}
	patchTimes         map[string]map[string]time.Time
	cancelMu           sync.Mutex
	cancels            map[string]context.CancelFunc
	cancelTimes        map[string]time.Time
	// lastEventID tracks the most recent audit event ID per session for chain linking.
	lastEventMu sync.Mutex
	lastEventID map[string]string
	// localStateTTL controls how long abandoned in-process state is retained.
	localStateTTL   time.Duration
	requireWorkItem bool
}

// rememberEventID stores the latest audit event ID for a session.
func (s *SessionService) rememberEventID(sessionID, eventID string) {
	if s == nil || sessionID == "" || eventID == "" {
		return
	}
	s.lastEventMu.Lock()
	defer s.lastEventMu.Unlock()
	if s.lastEventID == nil {
		s.lastEventID = make(map[string]string)
	}
	s.lastEventID[sessionID] = eventID
}

// parentEventID returns the last known audit event ID for a session.
func (s *SessionService) parentEventID(sessionID string) string {
	if s == nil {
		return ""
	}
	s.lastEventMu.Lock()
	defer s.lastEventMu.Unlock()
	return s.lastEventID[sessionID]
}

func (s *SessionService) registerCancel(sessionID string, cancel context.CancelFunc) {
	if s == nil || sessionID == "" {
		return
	}
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.cancels == nil {
		s.cancels = make(map[string]context.CancelFunc)
		s.cancelTimes = make(map[string]time.Time)
	}
	s.cancels[sessionID] = cancel
	s.cancelTimes[sessionID] = time.Now().UTC()
}

func (s *SessionService) cancelExecution(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	s.evictCancelsLocked()
	if cancel, ok := s.cancels[sessionID]; ok {
		cancel()
		delete(s.cancels, sessionID)
		delete(s.cancelTimes, sessionID)
	}
}

func (s *SessionService) evictCancelsLocked() {
	if s == nil || s.localStateTTL <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-s.localStateTTL)
	for id, t := range s.cancelTimes {
		if t.Before(cutoff) {
			delete(s.cancels, id)
			delete(s.cancelTimes, id)
		}
	}
}

func (s *SessionService) setSessionStatus(ctx context.Context, sessionID string, status string) {
	if s == nil || s.sessions == nil || sessionID == "" || status == "" {
		return
	}
	_ = s.sessions.UpdateStatus(ctx, sessionID, status)
}

func (s *SessionService) setRoutedAgent(ctx context.Context, sessionID string, agent string) error {
	if s == nil || s.sessions == nil || sessionID == "" || agent == "" {
		return nil
	}
	setter, ok := s.sessions.(interface {
		SetRoutedAgent(context.Context, string, string) error
	})
	if !ok {
		return nil
	}
	return setter.SetRoutedAgent(ctx, sessionID, agent)
}

// SessionExists verifies that a session id is known to the Governance Shell.
func (s *SessionService) SessionExists(ctx context.Context, sessionID string) bool {
	if s == nil || sessionID == "" {
		return false
	}
	if s.sessions != nil {
		_, err := s.sessions.Get(ctx, sessionID)
		return err == nil
	}
	_, ok := s.promptForSession(sessionID)
	return ok
}

// SessionRecord returns the durable session record for server-side policy
// decisions. It deliberately does not fall back to process-local prompt state.
func (s *SessionService) SessionRecord(ctx context.Context, sessionID string) (SessionRecord, error) {
	if s == nil || s.sessions == nil || sessionID == "" {
		return SessionRecord{}, errors.New("session not found")
	}
	return s.sessions.Get(ctx, sessionID)
}

type AutoGatewaySessionRequest struct {
	ActorSubject       string
	Classification     string
	PromptSHA256       string
	ModelAlias         string
	Client             string
	Endpoint           string
	RawRequestBody     []byte
	TrustedClientToken string
	UseCaseID          string
	WorkflowID         string
	WorkItemID         string
	WorkItemType       string
	RepoURL            string
	Branch             string
	CommitSHA          string
	Intent             string
	ActorHint          string
	SourceSystem       string
	EstimatedCostUSD   float64
}

type AutoGatewaySessionResult struct {
	Record       SessionRecord
	GatewayToken string
}

func (s *SessionService) CreateAutoGatewaySession(ctx context.Context, req AutoGatewaySessionRequest) (AutoGatewaySessionResult, error) {
	if s == nil || s.sessions == nil || s.audit == nil {
		return AutoGatewaySessionResult{}, errors.New("auto sessions unavailable")
	}
	actorSubject := strings.TrimSpace(req.ActorSubject)
	if actorSubject == "" || !validActorLabel(actorSubject) {
		return AutoGatewaySessionResult{}, errors.New("valid actor subject is required")
	}
	classification := strings.TrimSpace(req.Classification)
	if classification == "" {
		classification = "internal"
	}
	request := CreateSessionRequest{
		Agent:            "model-gateway",
		Classification:   classification,
		Prompt:           string(req.RawRequestBody),
		EstimatedCostUSD: req.EstimatedCostUSD,
		PermissionMode:   "reviewed",
		ApprovalMode:     "self_reported",
		WorkspaceMode:    "client-local",
		UseCaseID:        strings.TrimSpace(req.UseCaseID),
		WorkflowID:       strings.TrimSpace(req.WorkflowID),
		WorkItemID:       strings.TrimSpace(req.WorkItemID),
		WorkItemType:     strings.TrimSpace(req.WorkItemType),
		RepoURL:          strings.TrimSpace(req.RepoURL),
		Branch:           strings.TrimSpace(req.Branch),
		CommitSHA:        strings.TrimSpace(req.CommitSHA),
		Intent:           strings.TrimSpace(req.Intent),
		ActorHint:        strings.TrimSpace(req.ActorHint),
		SourceSystem:     defaultString(strings.TrimSpace(req.SourceSystem), defaultString(strings.TrimSpace(req.Client), "model-gateway")),
	}
	request.normalizeRuntimeModes()
	if strings.TrimSpace(request.Prompt) == "" {
		request.Prompt = strings.TrimSpace(req.PromptSHA256)
	}
	if blocked, reason := s.blockedByKillSwitch(""); blocked {
		_ = s.appendDenied(WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"}), reason, nil, classification)
		return AutoGatewaySessionResult{}, errors.New(reason)
	}
	if err := s.enforceWorkItemContext(&request); err != nil {
		_ = s.appendDenied(WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"}), err.Error(), nil, classification)
		return AutoGatewaySessionResult{}, err
	}
	if blocked, reason := s.blockedByKillSwitch(request.Agent); blocked {
		_ = s.appendDenied(WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"}), reason, nil, classification)
		return AutoGatewaySessionResult{}, errors.New(reason)
	}
	if blocked, reason := s.blockedByClientKillSwitch(strings.TrimSpace(req.Client)); blocked {
		_ = s.appendDenied(WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"}), reason, nil, classification)
		return AutoGatewaySessionResult{}, errors.New(reason)
	}
	policyCtx := WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"})
	decision, err := s.evaluatePolicy(policyCtx, policyengine.Request{
		AgentName:         request.Agent,
		ActionType:        "session.auto_create",
		Classification:    request.Classification,
		ClassificationMax: s.classificationMax,
		Metadata: map[string]any{
			"prompt_length": len(request.Prompt),
			"prompt":        request.Prompt,
			"model_alias":   strings.TrimSpace(req.ModelAlias),
			"client":        strings.TrimSpace(req.Client),
			"endpoint":      strings.TrimSpace(req.Endpoint),
		},
		CostCapEnabled:    s.costCapEnabled,
		SessionCostCapUSD: s.sessionCostCapUSD,
		EstimatedCostUSD:  request.EstimatedCostUSD,
	})
	if err != nil {
		_ = s.appendDenied(policyCtx, "policy engine unavailable", nil, classification)
		return AutoGatewaySessionResult{}, errors.New("policy engine unavailable")
	}
	if !decision.Allowed {
		reason := decision.Reason
		if reason == "" {
			reason = "policy denied"
		}
		if reason == "cost cap exceeded" {
			_ = s.appendDeniedWithCost(policyCtx, reason, classification, request.EstimatedCostUSD, s.sessionCostCapUSD)
			s.recordCostCapped()
		} else {
			_ = s.appendDenied(policyCtx, reason, decision.Findings, classification)
			s.recordPolicyDenial(reason)
		}
		return AutoGatewaySessionResult{}, errors.New(reason)
	}
	promptHash := strings.TrimSpace(req.PromptSHA256)
	modelAlias := strings.TrimSpace(req.ModelAlias)
	endpoint := strings.TrimSpace(req.Endpoint)
	sessionID := s.newID("sess_auto")
	eventID := s.newID("evt")
	now := time.Now().UTC()
	trust := s.trustMetadataFromClient(strings.TrimSpace(req.Client), strings.TrimSpace(req.TrustedClientToken))
	gatewayToken := s.newID("sgt")
	tokenHash := sha256.Sum256([]byte(gatewayToken))
	event, err := s.audit.Append(ctx, audit.Event{
		EventID:            eventID,
		SessionID:          sessionID,
		EventType:          "session.auto_created",
		Actor:              actorSubject,
		Agent:              request.Agent,
		Classification:     classification,
		PermissionMode:     request.PermissionMode,
		ApprovalMode:       request.ApprovalMode,
		WorkspaceMode:      request.WorkspaceMode,
		WorkItemID:         request.WorkItemID,
		WorkItemType:       request.WorkItemType,
		CommitSHA:          request.CommitSHA,
		ActorHint:          request.ActorHint,
		SourceSystem:       request.SourceSystem,
		PromptSHA256:       promptHash,
		ModelAlias:         modelAlias,
		Reason:             "auto session for model gateway " + endpoint,
		EstimatedCostUSD:   request.EstimatedCostUSD,
		CostCapUSD:         activeCostCap(s.costCapEnabled, s.sessionCostCapUSD),
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "model-gateway",
		TrustLevel:         trust.TrustLevel,
		EnforcementMode:    trust.EnforcementMode,
		RecordedAt:         now,
	})
	if err != nil {
		return AutoGatewaySessionResult{}, err
	}
	rec := SessionRecord{
		SessionID:          sessionID,
		GatewayTokenSHA256: hex.EncodeToString(tokenHash[:]),
		ActorSubject:       actorSubject,
		Agent:              request.Agent,
		Classification:     classification,
		PromptSHA256:       promptHash,
		Status:             "running",
		CreatedAt:          now,
		PermissionMode:     request.PermissionMode,
		ApprovalMode:       request.ApprovalMode,
		WorkspaceMode:      request.WorkspaceMode,
		UseCaseID:          request.UseCaseID,
		WorkflowID:         request.WorkflowID,
		WorkItemID:         request.WorkItemID,
		WorkItemType:       request.WorkItemType,
		RepoURL:            request.RepoURL,
		Branch:             request.Branch,
		CommitSHA:          request.CommitSHA,
		Intent:             request.Intent,
		ActorHint:          request.ActorHint,
		SourceSystem:       request.SourceSystem,
	}
	if err := s.sessions.Create(ctx, rec); err != nil {
		return AutoGatewaySessionResult{}, err
	}
	s.rememberEventID(sessionID, event.EventID)
	s.recordSessionCreated()
	return AutoGatewaySessionResult{Record: rec, GatewayToken: gatewayToken}, nil
}

type CreateSessionRequest struct {
	Agent            string  `json:"agent"`
	Classification   string  `json:"classification"`
	Prompt           string  `json:"prompt"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd,omitempty"`
	RunID            string  `json:"run_id,omitempty"`
	PermissionMode   string  `json:"permission_mode,omitempty"`
	ApprovalMode     string  `json:"approval_mode,omitempty"`
	WorkspaceMode    string  `json:"workspace_mode,omitempty"`
	// Control-plane bindings.
	UseCaseID    string `json:"use_case_id,omitempty"`
	WorkflowID   string `json:"workflow_id,omitempty"`
	WorkItemID   string `json:"work_item_id,omitempty"`
	WorkItemType string `json:"work_item_type,omitempty"`
	RepoURL      string `json:"repo_url,omitempty"`
	Branch       string `json:"branch,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	Intent       string `json:"intent,omitempty"`
	ActorHint    string `json:"actor_hint,omitempty"`
	SourceSystem string `json:"source_system,omitempty"`
	// Cost/value sizing.
	StoryPoints         int     `json:"story_points,omitempty"`
	EstimatedDevDays    float64 `json:"estimated_dev_days,omitempty"`
	BlendedDayRateUSD   float64 `json:"blended_day_rate_usd,omitempty"`
	BaselineCostUSD     float64 `json:"baseline_cost_usd,omitempty"`
	ModelCostUSD        float64 `json:"model_cost_usd,omitempty"`
	ToolCostUSD         float64 `json:"tool_cost_usd,omitempty"`
	PlatformCostUSD     float64 `json:"platform_cost_usd,omitempty"`
	ReviewCostUSD       float64 `json:"review_cost_usd,omitempty"`
	VerificationCostUSD float64 `json:"verification_cost_usd,omitempty"`
	RetryCount          int     `json:"retry_count,omitempty"`
}

type CreateSessionResponse struct {
	SessionID      string `json:"session_id"`
	RunID          string `json:"run_id,omitempty"`
	Status         string `json:"status"`
	Agent          string `json:"agent"`
	PermissionMode string `json:"permission_mode,omitempty"`
	ApprovalMode   string `json:"approval_mode,omitempty"`
	AuditEventID   string `json:"audit_event_id"`
	// GatewayToken is the per-session secret runtimes must send as
	// X-AI-Orch-Session-Token on model gateway calls. It is returned once at
	// creation; only its hash is stored.
	GatewayToken string `json:"gateway_token,omitempty"`
}

type SessionSummary struct {
	SessionID           string              `json:"session_id"`
	RunID               string              `json:"run_id,omitempty"`
	ActorSubject        string              `json:"actor_subject"`
	Agent               string              `json:"agent"`
	RoutedAgent         string              `json:"routed_agent,omitempty"`
	Classification      string              `json:"classification"`
	Status              string              `json:"status"`
	CreatedAt           time.Time           `json:"created_at"`
	PermissionMode      string              `json:"permission_mode,omitempty"`
	ApprovalMode        string              `json:"approval_mode,omitempty"`
	WorkspaceMode       string              `json:"workspace_mode,omitempty"`
	UseCaseID           string              `json:"use_case_id,omitempty"`
	WorkflowID          string              `json:"workflow_id,omitempty"`
	WorkItemID          string              `json:"work_item_id,omitempty"`
	WorkItemType        string              `json:"work_item_type,omitempty"`
	RepoURL             string              `json:"repo_url,omitempty"`
	Branch              string              `json:"branch,omitempty"`
	CommitSHA           string              `json:"commit_sha,omitempty"`
	Intent              string              `json:"intent,omitempty"`
	ActorHint           string              `json:"actor_hint,omitempty"`
	SourceSystem        string              `json:"source_system,omitempty"`
	StoryPoints         int                 `json:"story_points,omitempty"`
	EstimatedDevDays    float64             `json:"estimated_dev_days,omitempty"`
	BlendedDayRateUSD   float64             `json:"blended_day_rate_usd,omitempty"`
	BaselineCostUSD     float64             `json:"baseline_cost_usd,omitempty"`
	ModelCostUSD        float64             `json:"model_cost_usd,omitempty"`
	ToolCostUSD         float64             `json:"tool_cost_usd,omitempty"`
	PlatformCostUSD     float64             `json:"platform_cost_usd,omitempty"`
	ReviewCostUSD       float64             `json:"review_cost_usd,omitempty"`
	VerificationCostUSD float64             `json:"verification_cost_usd,omitempty"`
	RetryCount          int                 `json:"retry_count,omitempty"`
	UsageSummary        SessionUsageSummary `json:"usage_summary"`
	LatestEventType     string              `json:"latest_event_type,omitempty"`
	LatestEventAt       time.Time           `json:"latest_event_at,omitempty"`
	Transport           string              `json:"transport,omitempty"`
	TrustLevel          string              `json:"trust_level,omitempty"`
	EnforcementMode     string              `json:"enforcement_mode,omitempty"`
	PatchState          string              `json:"patch_state,omitempty"`
	PatchCount          int                 `json:"patch_count,omitempty"`
	ToolCallCount       int                 `json:"tool_call_count,omitempty"`
	PolicyDecision      string              `json:"policy_decision,omitempty"`
	PolicyReason        string              `json:"policy_reason,omitempty"`
}

type ListSessionsResponse struct {
	Sessions []SessionSummary `json:"sessions"`
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
	ttl := cfg.LocalStateTTL
	if ttl <= 0 {
		ttl = defaultLocalStateTTL
	}
	auditReader := cfg.AuditReader
	if auditReader == nil {
		if reader, ok := cfg.Audit.(AuditReader); ok {
			auditReader = reader
		}
	}
	return &SessionService{
		devToken:           cfg.DevToken,
		adminToken:         cfg.AdminToken,
		authorizer:         cfg.Authorizer,
		audit:              cfg.Audit,
		auditReader:        auditReader,
		sessions:           cfg.Sessions,
		modelPricing:       cfg.ModelPricing,
		classificationMax:  defaultString(cfg.ClassificationMax, "internal"),
		killSwitch:         cfg.KillSwitch,
		killSwitchStore:    cfg.KillSwitchStore,
		costCapEnabled:     cfg.CostCapEnabled,
		sessionCostCapUSD:  cfg.SessionCostCapUSD,
		policyEngine:       engine,
		toolLoopMax:        toolLoopMax,
		patchBuffer:        patchBuffer,
		metrics:            cfg.Metrics,
		newID:              newID,
		contextResolver:    cfg.ContextResolver,
		trustedClientToken: cfg.TrustedClientToken,
		prompts:            make(map[string]string),
		promptTimes:        make(map[string]time.Time),
		patches:            make(map[string]map[string]struct{}),
		patchTimes:         make(map[string]map[string]time.Time),
		cancels:            make(map[string]context.CancelFunc),
		cancelTimes:        make(map[string]time.Time),
		localStateTTL:      ttl,
		requireWorkItem:    cfg.RequireWorkItem,
	}
}

func NewSessionHandler(service *SessionService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", service.sessionsCollection)
	return mux
}

func (s *SessionService) sessionsCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createSession(w, r)
	case http.MethodGet:
		s.listSessions(w, r)
	default:
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (s *SessionService) listSessions(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.sessions == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return
	}
	authReq, ok := s.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	limit := parseSessionListLimit(authReq.URL.Query().Get("limit"))
	records, err := s.sessions.ListRecent(authReq.Context(), actorFromContext(authReq.Context()), limit)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session list failed"})
		return
	}
	summaries := make([]SessionSummary, 0, len(records))
	for _, record := range records {
		summary := sessionSummaryFromRecord(record)
		if s.auditReader != nil {
			events, err := s.auditReader.EventsBySession(authReq.Context(), record.SessionID)
			if err != nil {
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session usage lookup failed"})
				return
			}
			summary.UsageSummary = SummarizeSessionUsageWithPricing(authReq.Context(), events, s.modelPricing)
			applyLedgerFields(&summary, events)
		}
		summaries = append(summaries, summary)
	}
	httpx.WriteJSON(w, http.StatusOK, ListSessionsResponse{Sessions: summaries})
}

func applyLedgerFields(summary *SessionSummary, events []audit.Event) {
	if summary == nil || len(events) == 0 {
		return
	}
	for _, event := range events {
		if event.RecordedAt.After(summary.LatestEventAt) {
			summary.LatestEventAt = event.RecordedAt
			summary.LatestEventType = event.EventType
		}
		if event.TrustLevel != "" {
			summary.TrustLevel = event.TrustLevel
		}
		if event.EnforcementMode != "" {
			summary.EnforcementMode = event.EnforcementMode
		}
		if event.Runtime != "" {
			summary.Transport = event.Runtime
		}
		if event.GatewayBackend != "" {
			summary.Transport = "model-gateway/" + event.GatewayBackend
		}
		if event.PolicyDecisionID != "" {
			summary.PolicyDecision = event.PolicyDecisionID
		}
		if event.Reason != "" && (strings.Contains(event.EventType, "denied") || event.EventType == "session.created" || event.EventType == "session.auto_created") {
			summary.PolicyReason = event.Reason
		}
		if event.PatchID != "" || event.PatchCount > 0 {
			if event.PatchCount > summary.PatchCount {
				summary.PatchCount = event.PatchCount
			}
			if event.PatchID != "" {
				summary.PatchCount++
			}
		}
		if event.PatchDecision != "" {
			summary.PatchState = event.PatchDecision
		} else if event.PatchID != "" && summary.PatchState == "" {
			summary.PatchState = "proposed"
		}
		if event.ToolCallCount > 0 {
			summary.ToolCallCount += event.ToolCallCount
		}
		if event.EventType == "mcp.proxy_call" && event.Reason == "forwarded" {
			summary.ToolCallCount++
		}
	}
	if summary.Transport == "" {
		summary.Transport = summary.SourceSystem
	}
}

func parseSessionListLimit(value string) int {
	if value == "" {
		return 20
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func sessionSummaryFromRecord(record SessionRecord) SessionSummary {
	return SessionSummary{
		SessionID:           record.SessionID,
		RunID:               record.RunID,
		ActorSubject:        record.ActorSubject,
		Agent:               record.Agent,
		RoutedAgent:         record.RoutedAgent,
		Classification:      record.Classification,
		Status:              record.Status,
		CreatedAt:           record.CreatedAt,
		PermissionMode:      record.PermissionMode,
		ApprovalMode:        record.ApprovalMode,
		WorkspaceMode:       record.WorkspaceMode,
		UseCaseID:           record.UseCaseID,
		WorkflowID:          record.WorkflowID,
		WorkItemID:          record.WorkItemID,
		WorkItemType:        record.WorkItemType,
		RepoURL:             record.RepoURL,
		Branch:              record.Branch,
		CommitSHA:           record.CommitSHA,
		Intent:              record.Intent,
		ActorHint:           record.ActorHint,
		SourceSystem:        record.SourceSystem,
		StoryPoints:         record.StoryPoints,
		EstimatedDevDays:    record.EstimatedDevDays,
		BlendedDayRateUSD:   record.BlendedDayRateUSD,
		BaselineCostUSD:     record.BaselineCostUSD,
		ModelCostUSD:        record.ModelCostUSD,
		ToolCostUSD:         record.ToolCostUSD,
		PlatformCostUSD:     record.PlatformCostUSD,
		ReviewCostUSD:       record.ReviewCostUSD,
		VerificationCostUSD: record.VerificationCostUSD,
		RetryCount:          record.RetryCount,
	}
}

func (s *SessionService) createSession(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.audit == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	authReq, ok := s.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq
	if blocked, reason := s.blockedByKillSwitch(""); blocked {
		if err := s.appendDenied(r.Context(), reason, nil, ""); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		httpx.WriteJSON(w, http.StatusLocked, map[string]any{"error": reason})
		return
	}

	var request CreateSessionRequest
	if err := readJSON(w, r, &request); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	request.normalizeRuntimeModes()
	if err := request.validate(); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.resolveSessionContext(&request)
	if err := s.enforceWorkItemContext(&request); err != nil {
		if auditErr := s.appendDenied(r.Context(), err.Error(), nil, request.Classification); auditErr != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		httpx.WriteJSON(w, http.StatusPreconditionRequired, map[string]any{"error": err.Error()})
		return
	}
	if blocked, reason := s.blockedByKillSwitch(request.Agent); blocked {
		if err := s.appendDenied(r.Context(), reason, nil, request.Classification); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		httpx.WriteJSON(w, http.StatusLocked, map[string]any{"error": reason})
		return
	}
	decision, err := s.evaluatePolicy(r.Context(), policyengine.Request{
		AgentName:         request.Agent,
		ActionType:        "session.create",
		Classification:    request.Classification,
		ClassificationMax: s.classificationMax,
		Metadata: map[string]any{
			"prompt_length": len(request.Prompt),
			"prompt":        request.Prompt,
		},
		CostCapEnabled:    s.costCapEnabled,
		SessionCostCapUSD: s.sessionCostCapUSD,
		EstimatedCostUSD:  request.EstimatedCostUSD,
	})
	if err != nil {
		if auditErr := s.appendDenied(r.Context(), "policy engine unavailable", nil, request.Classification); auditErr != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "policy engine unavailable"})
		return
	}
	if !decision.Allowed {
		reason := decision.Reason
		if reason == "" {
			reason = "policy denied"
		}
		findings := decision.Findings
		if reason == "cost cap exceeded" {
			if err := s.appendDeniedWithCost(r.Context(), reason, request.Classification, request.EstimatedCostUSD, s.sessionCostCapUSD); err != nil {
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
				return
			}
			s.recordCostCapped()
			httpx.WriteJSON(w, http.StatusPaymentRequired, map[string]any{"error": reason})
			return
		}
		if err := s.appendDenied(r.Context(), reason, findings, request.Classification); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		s.recordPolicyDenial(reason)
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": reason})
		return
	}

	sessionID := s.newID("sess")
	eventID := s.newID("evt")
	promptHash := sha256.Sum256([]byte(request.Prompt))

	actor := actorFromContext(r.Context())
	trust := s.trustMetadataFromRequest(r)

	event, err := s.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		SessionID:          sessionID,
		EventType:          "session.created",
		Actor:              actor,
		Agent:              request.Agent,
		Classification:     request.Classification,
		RunID:              request.RunID,
		PermissionMode:     request.PermissionMode,
		ApprovalMode:       request.ApprovalMode,
		WorkspaceMode:      request.WorkspaceMode,
		WorkItemID:         request.WorkItemID,
		WorkItemType:       request.WorkItemType,
		CommitSHA:          request.CommitSHA,
		ActorHint:          request.ActorHint,
		SourceSystem:       request.SourceSystem,
		PromptSHA256:       hex.EncodeToString(promptHash[:]),
		EstimatedCostUSD:   request.EstimatedCostUSD,
		CostCapUSD:         activeCostCap(s.costCapEnabled, s.sessionCostCapUSD),
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         trust.TrustLevel,
		EnforcementMode:    trust.EnforcementMode,
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}

	// Per-session gateway secret: returned once to the caller, stored hashed.
	// Runtimes present it as X-AI-Orch-Session-Token so a leaked session ID
	// plus the shared runtime token is not enough to bill another actor.
	gatewayToken := ""
	gatewayTokenHash := ""
	if s.sessions != nil {
		gatewayToken = s.newID("sgt")
		sum := sha256.Sum256([]byte(gatewayToken))
		gatewayTokenHash = hex.EncodeToString(sum[:])
	}

	// Persist only after the authoritative audit event is durable.
	if s.sessions != nil {
		if err := s.sessions.Create(r.Context(), SessionRecord{
			SessionID:           sessionID,
			RunID:               request.RunID,
			GatewayTokenSHA256:  gatewayTokenHash,
			ActorSubject:        actor,
			Agent:               request.Agent,
			Classification:      request.Classification,
			PromptSHA256:        hex.EncodeToString(promptHash[:]),
			Status:              "created",
			CreatedAt:           time.Now().UTC(),
			PermissionMode:      request.PermissionMode,
			ApprovalMode:        request.ApprovalMode,
			WorkspaceMode:       request.WorkspaceMode,
			UseCaseID:           request.UseCaseID,
			WorkflowID:          request.WorkflowID,
			WorkItemID:          request.WorkItemID,
			WorkItemType:        request.WorkItemType,
			RepoURL:             request.RepoURL,
			Branch:              request.Branch,
			CommitSHA:           request.CommitSHA,
			Intent:              request.Intent,
			ActorHint:           request.ActorHint,
			SourceSystem:        request.SourceSystem,
			StoryPoints:         request.StoryPoints,
			EstimatedDevDays:    request.EstimatedDevDays,
			BlendedDayRateUSD:   request.BlendedDayRateUSD,
			BaselineCostUSD:     request.BaselineCostUSD,
			ModelCostUSD:        request.ModelCostUSD,
			ToolCostUSD:         request.ToolCostUSD,
			PlatformCostUSD:     request.PlatformCostUSD,
			ReviewCostUSD:       request.ReviewCostUSD,
			VerificationCostUSD: request.VerificationCostUSD,
			RetryCount:          request.RetryCount,
		}); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session store failed"})
			return
		}
	}
	s.recordSessionCreated()
	s.rememberPrompt(sessionID, request.Prompt)
	s.rememberEventID(sessionID, event.EventID)

	httpx.WriteJSON(w, http.StatusCreated, CreateSessionResponse{
		SessionID:      sessionID,
		RunID:          request.RunID,
		Status:         "created",
		Agent:          request.Agent,
		PermissionMode: request.PermissionMode,
		ApprovalMode:   request.ApprovalMode,
		AuditEventID:   event.EventID,
		GatewayToken:   gatewayToken,
	})
}

func (s *SessionService) resolveSessionContext(request *CreateSessionRequest) {
	if s == nil || s.contextResolver == nil || request == nil {
		return
	}
	if request.RepoURL != "" && request.Branch != "" && request.CommitSHA != "" &&
		request.WorkItemID != "" && request.WorkItemType != "" &&
		request.ActorHint != "" && request.SourceSystem != "" {
		return
	}
	resolved := s.contextResolver.Resolve()
	if request.RepoURL == "" {
		request.RepoURL = resolved.RepoURL
	}
	if request.Branch == "" {
		request.Branch = resolved.Branch
	}
	if request.CommitSHA == "" {
		request.CommitSHA = resolved.CommitSHA
	}
	if request.WorkItemID == "" {
		request.WorkItemID = resolved.WorkItemID
	}
	if request.WorkItemType == "" {
		request.WorkItemType = resolved.WorkItemType
	}
	if request.ActorHint == "" {
		request.ActorHint = resolved.ActorHint
	}
	if request.SourceSystem == "" {
		request.SourceSystem = resolved.SourceSystem
	}
}

func (s *SessionService) enforceWorkItemContext(request *CreateSessionRequest) error {
	if s == nil || !s.requireWorkItem || request == nil {
		return nil
	}
	workItemID := strings.TrimSpace(request.WorkItemID)
	if workItemID == "" {
		return errors.New("work item ID is required; switch to a feature branch named after the work item (for example feature/<WORK-ITEM-ID>-short-description) or pass work_item_id")
	}
	branch := strings.TrimSpace(request.Branch)
	if branch == "" {
		return fmt.Errorf("feature branch is required before starting governed work; switch to a branch containing %s", workItemID)
	}
	switch strings.ToLower(branch) {
	case "main", "master", "trunk", "develop", "development":
		return fmt.Errorf("governed work cannot start on protected branch %q; switch to a feature branch containing %s", branch, workItemID)
	}
	if !strings.Contains(strings.ToLower(branch), strings.ToLower(workItemID)) {
		return fmt.Errorf("branch %q must contain work item ID %q", branch, workItemID)
	}
	return nil
}

// mintRuntimeGatewayToken creates the dispatch-time gateway secret for
// server-side runtimes and persists its hash on the session record. Returns
// the raw token, or empty when the session store cannot record the hash, in
// which case the runtime falls back to legacy shared-token behavior.
func (s *SessionService) mintRuntimeGatewayToken(ctx context.Context, sessionID string) string {
	if s == nil || s.sessions == nil {
		return ""
	}
	setter, ok := s.sessions.(interface {
		SetRuntimeGatewayTokenHash(context.Context, string, string) error
	})
	if !ok {
		return ""
	}
	token := s.newID("srt")
	sum := sha256.Sum256([]byte(token))
	if err := setter.SetRuntimeGatewayTokenHash(ctx, sessionID, hex.EncodeToString(sum[:])); err != nil {
		return ""
	}
	return token
}

// AdminOperatorSubject is the synthetic actor for admin-token requests. It
// grants org-wide read access on actor-scoped surfaces.
const AdminOperatorSubject = "admin-operator"

// AdminBearerSubject reports whether the Authorization header carries the
// configured admin token.
func (s *SessionService) AdminBearerSubject(header string) (string, bool) {
	if s == nil || s.adminToken == "" {
		return "", false
	}
	if !strings.HasPrefix(header, "Bearer ") {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(s.adminToken)) != 1 {
		return "", false
	}
	return AdminOperatorSubject, true
}

// actorFromContext extracts the authenticated subject from the context, falling back to "local-dev".
func actorFromContext(ctx context.Context) string {
	if info, ok := AuthInfoFromContext(ctx); ok && info.Subject != "" {
		return info.Subject
	}
	return "local-dev"
}

type requestTrustMetadata struct {
	TrustLevel      string
	EnforcementMode string
}

// trustMetadataFromRequest derives the trust level recorded on an audit event.
// Trust is server-authoritative: the X-AI-Orch-Client header is a non-authoritative
// claim of client identity, and a self-declared X-AI-Orch-Trust-Level header is never
// honored. When a trusted-client token is configured, the privileged levels
// (gateway_enforced, managed_client) are only granted to callers that prove
// possession of that shared secret, so an ordinary token holder cannot forge a
// stronger trust label on the audit trail. When no token is configured (local dev),
// the client identity header is honored on its own for backward compatibility.
func (s *SessionService) trustMetadataFromRequest(r *http.Request) requestTrustMetadata {
	selfReported := requestTrustMetadata{TrustLevel: "self_reported", EnforcementMode: "advisory"}
	if r == nil {
		return selfReported
	}
	return s.trustMetadataFromClient(r.Header.Get("X-AI-Orch-Client"), r.Header.Get("X-AI-Orch-Trusted-Client-Token"))
}

func (s *SessionService) trustMetadataFromClient(client string, presentedToken string) requestTrustMetadata {
	selfReported := requestTrustMetadata{TrustLevel: "self_reported", EnforcementMode: "advisory"}
	if s != nil && s.trustedClientToken != "" {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(presentedToken)), []byte(s.trustedClientToken)) != 1 {
			return selfReported
		}
	}
	client = strings.ToLower(strings.TrimSpace(client))
	switch client {
	case "ai-orch-mcp":
		return requestTrustMetadata{TrustLevel: "gateway_enforced", EnforcementMode: "gateway"}
	case "ai-agent-bridge", "vscode-bridge", "ai-orch-bridge":
		return requestTrustMetadata{TrustLevel: "managed_client", EnforcementMode: "managed"}
	}
	// Unknown clients may report evidence, but they cannot upgrade its strength.
	return selfReported
}

func (s *SessionService) recordPolicyDenial(reason string) {
	if s == nil {
		return
	}
	switch reason {
	case "secret detected":
		s.recordSecretBlocked()
	default:
		if strings.HasPrefix(reason, "classification ") || strings.HasPrefix(reason, "unknown classification") {
			s.recordClassificationBlocked()
		}
	}
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
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return r, false
	}
	// The auth middleware may already have established identity (including the
	// admin-operator superset); handlers re-checking must honor it.
	if info, ok := AuthInfoFromContext(r.Context()); ok && info.Subject != "" {
		return r, true
	}
	if s.authorizer != nil {
		subject, ok := s.authorizer.Validate(r.Context(), r.Header.Get("Authorization"))
		if ok {
			r = r.WithContext(WithAuthInfo(r.Context(), AuthInfo{Subject: subject, Method: "oidc"}))
			return r, true
		}
		if err := s.appendDenied(r.Context(), "invalid bearer token", nil, ""); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return r, false
		}
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return r, false
	}
	if s.devToken == "" {
		if err := s.appendDenied(r.Context(), "dev token not configured", nil, ""); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return r, false
		}
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "dev token not configured"})
		return r, false
	}
	if !s.authorized(r.Header.Get("Authorization")) {
		if err := s.appendDenied(r.Context(), "invalid dev token", nil, ""); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return r, false
		}
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return r, false
	}
	subject := "local-dev"
	// X-AI-Orch-Local-Identity is a dev-mode convenience header for local testing.
	// It allows the bridge or CLI to assert an actor label without OIDC.
	// In production, actor identity must come from OIDC claims, not client headers.
	if localIdentity := r.Header.Get("X-AI-Orch-Local-Identity"); localIdentity != "" {
		if !validActorLabel(localIdentity) {
			if err := s.appendDenied(r.Context(), "invalid local identity", nil, ""); err != nil {
				httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
				return r, false
			}
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid local identity"})
			return r, false
		}
		subject = localIdentity
	}
	r = r.WithContext(WithAuthInfo(r.Context(), AuthInfo{Subject: subject, Method: "dev"}))
	return r, true
}

// RequireAdminRequest validates the request carries the separate admin token.
// Admin endpoints (kill switch, audit retention) must use a token distinct from
// ordinary session auth. If no admin token is configured, admin endpoints are disabled.
func (s *SessionService) RequireAdminRequest(w http.ResponseWriter, r *http.Request) bool {
	if s == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return false
	}
	if s.adminToken == "" {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "admin not configured"})
		return false
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return false
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), []byte(s.adminToken)) != 1 {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "admin access required"})
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

func (s *SessionService) blockedByClientKillSwitch(client string) (bool, string) {
	if s == nil || s.killSwitchStore == nil {
		return false, ""
	}
	client = strings.TrimSpace(client)
	if client == "" {
		return false, ""
	}
	if s.killSwitchStore.IsBlocked("client", client) {
		return true, fmt.Sprintf("kill switch enabled for client %s", client)
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
	s.promptTimes[sessionID] = time.Now().UTC()
}

func (s *SessionService) promptForSession(sessionID string) (string, bool) {
	if s == nil || sessionID == "" {
		return "", false
	}
	s.promptMu.Lock()
	defer s.promptMu.Unlock()
	s.evictPromptsLocked()
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
	delete(s.promptTimes, sessionID)
}

func (s *SessionService) evictPromptsLocked() {
	if s == nil || s.localStateTTL <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-s.localStateTTL)
	for id, t := range s.promptTimes {
		if t.Before(cutoff) {
			delete(s.prompts, id)
			delete(s.promptTimes, id)
		}
	}
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
	if s.patchTimes[sessionID] == nil {
		s.patchTimes[sessionID] = make(map[string]time.Time)
	}
	s.patches[sessionID][patchID] = struct{}{}
	s.patchTimes[sessionID][patchID] = time.Now().UTC()
}

func (s *SessionService) patchKnown(sessionID string, patchID string) bool {
	if s == nil || sessionID == "" || patchID == "" {
		return false
	}
	s.patchMu.Lock()
	defer s.patchMu.Unlock()
	s.evictPatchesLocked()
	patches := s.patches[sessionID]
	_, ok := patches[patchID]
	return ok
}

func (s *SessionService) evictPatchesLocked() {
	if s == nil || s.localStateTTL <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-s.localStateTTL)
	for sessionID, patchTimes := range s.patchTimes {
		for patchID, t := range patchTimes {
			if t.Before(cutoff) {
				delete(s.patches[sessionID], patchID)
				delete(patchTimes, patchID)
			}
		}
		if len(s.patches[sessionID]) == 0 {
			delete(s.patches, sessionID)
		}
		if len(patchTimes) == 0 {
			delete(s.patchTimes, sessionID)
		}
	}
}

// authorizedBearer delegates to httpauth.AuthorizedBearer so there is a single
// constant-time bearer comparison shared across the shell and the standalone
// services. An empty configured token always fails closed.
func authorizedBearer(header string, token string) bool {
	return httpauth.AuthorizedBearer(header, token)
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
	if !validPermissionMode(r.PermissionMode) {
		return errors.New("permission_mode must be one of read_only, reviewed, auto_apply or full_access")
	}
	if !validApprovalMode(r.ApprovalMode) {
		return errors.New("approval_mode must be one of manual, auto_approved, yolo or self_reported")
	}
	return nil
}

func (r *CreateSessionRequest) normalizeRuntimeModes() {
	if r == nil {
		return
	}
	r.PermissionMode = strings.ToLower(strings.TrimSpace(r.PermissionMode))
	r.ApprovalMode = strings.ToLower(strings.TrimSpace(r.ApprovalMode))
	r.WorkspaceMode = strings.ToLower(strings.TrimSpace(r.WorkspaceMode))
	if r.PermissionMode == "" {
		r.PermissionMode = "reviewed"
	}
	if r.ApprovalMode == "" {
		if r.PermissionMode == "full_access" {
			r.ApprovalMode = "yolo"
		} else {
			r.ApprovalMode = "manual"
		}
	}
}

func validPermissionMode(value string) bool {
	switch value {
	case "read_only", "reviewed", "auto_apply", "full_access":
		return true
	default:
		return false
	}
}

func validApprovalMode(value string) bool {
	switch value {
	case "manual", "auto_approved", "yolo", "self_reported":
		return true
	default:
		return false
	}
}

func validActorLabel(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '@', r == ':', r == '-':
		default:
			return false
		}
	}
	return true
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

func activeCostCap(enabled bool, value float64) float64 {
	if !enabled {
		return 0
	}
	return value
}
