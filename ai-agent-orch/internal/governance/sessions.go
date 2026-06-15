package governance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
)

type AuditAppender interface {
	Append(context.Context, audit.Event) (audit.Event, error)
}

const authInfoKey authContextKey = "authInfo"

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
	// ExecutionTimeout caps the wall-clock time of a single governed runtime
	// dispatch. Zero or negative means the 10-minute default.
	ExecutionTimeout time.Duration
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
	localStateTTL    time.Duration
	requireWorkItem  bool
	executionTimeout time.Duration
}

func (s *SessionService) setSessionStatus(ctx context.Context, sessionID string, status string) {
	if s == nil || s.sessions == nil || sessionID == "" || status == "" {
		return
	}
	_ = s.sessions.UpdateStatus(ctx, sessionID, status)
}

func (s *SessionService) setRouteDecision(ctx context.Context, sessionID string, decision RouteDecision) error {
	if s == nil || s.sessions == nil || sessionID == "" || decision.Specialist == "" {
		return nil
	}
	if setter, ok := s.sessions.(interface {
		SetRouteDecision(context.Context, string, string, string, bool) error
	}); ok {
		return setter.SetRouteDecision(ctx, sessionID, decision.Specialist, decision.RoutingConfidence, decision.HumanConfirmationRequired)
	}
	if setter, ok := s.sessions.(interface {
		SetRoutedAgent(context.Context, string, string) error
	}); ok {
		return setter.SetRoutedAgent(ctx, sessionID, decision.Specialist)
	}
	return nil
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
	SessionID                 string              `json:"session_id"`
	ParentSessionID           string              `json:"parent_session_id,omitempty"`
	RunID                     string              `json:"run_id,omitempty"`
	ActorSubject              string              `json:"actor_subject"`
	Agent                     string              `json:"agent"`
	RoutedAgent               string              `json:"routed_agent,omitempty"`
	RoutingConfidence         string              `json:"routing_confidence,omitempty"`
	HumanConfirmationRequired bool                `json:"human_confirmation_required,omitempty"`
	Classification            string              `json:"classification"`
	Status                    string              `json:"status"`
	CreatedAt                 time.Time           `json:"created_at"`
	PermissionMode            string              `json:"permission_mode,omitempty"`
	ApprovalMode              string              `json:"approval_mode,omitempty"`
	WorkspaceMode             string              `json:"workspace_mode,omitempty"`
	UseCaseID                 string              `json:"use_case_id,omitempty"`
	WorkflowID                string              `json:"workflow_id,omitempty"`
	WorkItemID                string              `json:"work_item_id,omitempty"`
	WorkItemType              string              `json:"work_item_type,omitempty"`
	RepoURL                   string              `json:"repo_url,omitempty"`
	Branch                    string              `json:"branch,omitempty"`
	CommitSHA                 string              `json:"commit_sha,omitempty"`
	Intent                    string              `json:"intent,omitempty"`
	ActorHint                 string              `json:"actor_hint,omitempty"`
	SourceSystem              string              `json:"source_system,omitempty"`
	ClientSessionID           string              `json:"client_session_id,omitempty"`
	StoryPoints               int                 `json:"story_points,omitempty"`
	EstimatedDevDays          float64             `json:"estimated_dev_days,omitempty"`
	BlendedDayRateUSD         float64             `json:"blended_day_rate_usd,omitempty"`
	BaselineCostUSD           float64             `json:"baseline_cost_usd,omitempty"`
	ModelCostUSD              float64             `json:"model_cost_usd,omitempty"`
	ToolCostUSD               float64             `json:"tool_cost_usd,omitempty"`
	PlatformCostUSD           float64             `json:"platform_cost_usd,omitempty"`
	ReviewCostUSD             float64             `json:"review_cost_usd,omitempty"`
	VerificationCostUSD       float64             `json:"verification_cost_usd,omitempty"`
	RetryCount                int                 `json:"retry_count,omitempty"`
	UsageSummary              SessionUsageSummary `json:"usage_summary"`
	LatestEventType           string              `json:"latest_event_type,omitempty"`
	LatestEventAt             time.Time           `json:"latest_event_at,omitempty"`
	Transport                 string              `json:"transport,omitempty"`
	TrustLevel                string              `json:"trust_level,omitempty"`
	EnforcementMode           string              `json:"enforcement_mode,omitempty"`
	PatchState                string              `json:"patch_state,omitempty"`
	PatchCount                int                 `json:"patch_count,omitempty"`
	ToolCallCount             int                 `json:"tool_call_count,omitempty"`
	PolicyDecision            string              `json:"policy_decision,omitempty"`
	PolicyReason              string              `json:"policy_reason,omitempty"`
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
	executionTimeout := cfg.ExecutionTimeout
	if executionTimeout <= 0 {
		executionTimeout = 10 * time.Minute
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
		executionTimeout:   executionTimeout,
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
		SessionID:                 record.SessionID,
		ParentSessionID:           record.ParentSessionID,
		RunID:                     record.RunID,
		ActorSubject:              record.ActorSubject,
		Agent:                     record.Agent,
		RoutedAgent:               record.RoutedAgent,
		RoutingConfidence:         record.RoutingConfidence,
		HumanConfirmationRequired: record.HumanConfirmationRequired,
		Classification:            record.Classification,
		Status:                    record.Status,
		CreatedAt:                 record.CreatedAt,
		PermissionMode:            record.PermissionMode,
		ApprovalMode:              record.ApprovalMode,
		WorkspaceMode:             record.WorkspaceMode,
		UseCaseID:                 record.UseCaseID,
		WorkflowID:                record.WorkflowID,
		WorkItemID:                record.WorkItemID,
		WorkItemType:              record.WorkItemType,
		RepoURL:                   record.RepoURL,
		Branch:                    record.Branch,
		CommitSHA:                 record.CommitSHA,
		Intent:                    record.Intent,
		ActorHint:                 record.ActorHint,
		SourceSystem:              record.SourceSystem,
		ClientSessionID:           record.ClientSessionID,
		StoryPoints:               record.StoryPoints,
		EstimatedDevDays:          record.EstimatedDevDays,
		BlendedDayRateUSD:         record.BlendedDayRateUSD,
		BaselineCostUSD:           record.BaselineCostUSD,
		ModelCostUSD:              record.ModelCostUSD,
		ToolCostUSD:               record.ToolCostUSD,
		PlatformCostUSD:           record.PlatformCostUSD,
		ReviewCostUSD:             record.ReviewCostUSD,
		VerificationCostUSD:       record.VerificationCostUSD,
		RetryCount:                record.RetryCount,
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
	request.RepoURL = sanitizeRepoURL(request.RepoURL)
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
		RepoURL:            request.RepoURL,
		Branch:             request.Branch,
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
