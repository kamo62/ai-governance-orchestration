package governance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// UseCase represents a governed use-case registration.
type UseCase struct {
	ID              string    `json:"id"`
	Owner           string    `json:"owner"`
	Domain          string    `json:"domain"`
	ExpectedBenefit string    `json:"expected_benefit"`
	LinkedWorkItem  string    `json:"linked_work_item,omitempty"`
	Classification  string    `json:"classification"`
	RiskLevel       string    `json:"risk_level"`
	CreatedAt       time.Time `json:"created_at"`
}

// Workflow represents a governed workflow template.
type Workflow struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Stages      []string  `json:"stages,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ContextManifest records the bounded context brief and provenance for a session.
type ContextManifest struct {
	ID              string    `json:"id"`
	SessionID       string    `json:"session_id"`
	Summary         string    `json:"summary"`
	SourceSystem    string    `json:"source_system"`
	SourceObjectID  string    `json:"source_object_id"`
	SourcePath      string    `json:"source_path,omitempty"`
	Actor           string    `json:"actor"`
	AuthScope       string    `json:"auth_scope"`
	FetchedAt       time.Time `json:"fetched_at"`
	Freshness       string    `json:"freshness,omitempty"`
	Classification  string    `json:"classification"`
	CacheStatus     string    `json:"cache_status"`
	ChunkHashes     []string  `json:"chunk_hashes,omitempty"`
	InfluencedModel bool      `json:"influenced_model"`
	CreatedAt       time.Time `json:"created_at"`
}

// CacheOutcome records a session-scoped cache hit/miss event.
type CacheOutcome struct {
	ID                  string    `json:"id"`
	SessionID           string    `json:"session_id"`
	CacheScope          string    `json:"cache_scope"`
	CacheKeyHash        string    `json:"cache_key_hash"`
	Hit                 bool      `json:"hit"`
	EligibilityReason   string    `json:"eligibility_reason,omitempty"`
	InvalidationReason  string    `json:"invalidation_reason,omitempty"`
	TTLSeconds          int       `json:"ttl_seconds,omitempty"`
	SourceFreshness     string    `json:"source_freshness,omitempty"`
	Classification      string    `json:"classification,omitempty"`
	Actor               string    `json:"actor,omitempty"`
	Repository          string    `json:"repository,omitempty"`
	Workflow            string    `json:"workflow,omitempty"`
	EstimatedSavingsUSD float64   `json:"estimated_savings_usd,omitempty"`
	AvoidedTokens       int       `json:"avoided_tokens,omitempty"`
	AvoidedCalls        int       `json:"avoided_calls,omitempty"`
	RecordedAt          time.Time `json:"recorded_at"`
}

// EvidenceRecord records test results, review outputs, approvals and quality links.
type EvidenceRecord struct {
	ID                string    `json:"id"`
	SessionID         string    `json:"session_id"`
	EvidenceType      string    `json:"evidence_type"`
	Description       string    `json:"description"`
	TestResult        string    `json:"test_result,omitempty"`
	QualitySystemLink string    `json:"quality_system_link,omitempty"`
	SecurityFinding   string    `json:"security_finding,omitempty"`
	ApprovalReceipt   string    `json:"approval_receipt,omitempty"`
	PatchDecision     string    `json:"patch_decision,omitempty"`
	ExternalTicket    string    `json:"external_ticket,omitempty"`
	TrustLevel        string    `json:"trust_level,omitempty"`
	EnforcementMode   string    `json:"enforcement_mode,omitempty"`
	RecordedAt        time.Time `json:"recorded_at"`
}

// MaturityExportRecord is a single bounded fact for downstream maturity reporting.
type MaturityExportRecord struct {
	SessionID            string               `json:"session_id"`
	EventType            string               `json:"event_type"`
	Actor                string               `json:"actor"`
	Team                 string               `json:"team,omitempty"`
	UseCaseID            string               `json:"use_case_id,omitempty"`
	WorkflowID           string               `json:"workflow_id,omitempty"`
	WorkItemID           string               `json:"work_item_id,omitempty"`
	Repository           string               `json:"repository,omitempty"`
	Branch               string               `json:"branch,omitempty"`
	Commit               string               `json:"commit,omitempty"`
	Classification       string               `json:"classification,omitempty"`
	RiskLevel            string               `json:"risk_level,omitempty"`
	Agent                string               `json:"agent,omitempty"`
	Specialist           string               `json:"specialist,omitempty"`
	Runtime              string               `json:"runtime,omitempty"`
	ModelAlias           string               `json:"model_alias,omitempty"`
	ModelResolved        string               `json:"model_resolved,omitempty"`
	Timestamps           map[string]time.Time `json:"timestamps,omitempty"`
	FinalStatus          string               `json:"final_status,omitempty"`
	PolicyDecision       string               `json:"policy_decision,omitempty"`
	PolicyReason         string               `json:"policy_reason,omitempty"`
	SecretScanResult     string               `json:"secret_scan_result,omitempty"`
	KillSwitchResult     string               `json:"kill_switch_result,omitempty"`
	OAuthFailureResult   string               `json:"oauth_failure_result,omitempty"`
	ToolLoopCapResult    string               `json:"tool_loop_cap_result,omitempty"`
	CostCapResult        string               `json:"cost_cap_result,omitempty"`
	HumanGateDecision    string               `json:"human_gate_decision,omitempty"`
	BaselineCostUSD      float64              `json:"baseline_cost_usd,omitempty"`
	ModelCostUSD         float64              `json:"model_cost_usd,omitempty"`
	ToolCostUSD          float64              `json:"tool_cost_usd,omitempty"`
	PlatformCostUSD      float64              `json:"platform_cost_usd,omitempty"`
	ReviewCostUSD        float64              `json:"review_cost_usd,omitempty"`
	VerificationCostUSD  float64              `json:"verification_cost_usd,omitempty"`
	RetryCount           int                  `json:"retry_count,omitempty"`
	ContextManifestID    string               `json:"context_manifest_id,omitempty"`
	EvidenceLinks        []string             `json:"evidence_links,omitempty"`
	PatchDecision        string               `json:"patch_decision,omitempty"`
	QualityResult        string               `json:"quality_result,omitempty"`
	EvidenceCompleteness float64              `json:"evidence_completeness,omitempty"`
	CycleTimeSeconds     float64              `json:"cycle_time_seconds,omitempty"`
	BlockedEventCategory string               `json:"blocked_event_category,omitempty"`
	CacheHits            int                  `json:"cache_hits,omitempty"`
	CacheMisses          int                  `json:"cache_misses,omitempty"`
	CacheSavingsEstimate float64              `json:"cache_savings_estimate,omitempty"`
	RecordedAt           time.Time            `json:"recorded_at"`
}

// RegistryStore is an in-memory store for use-cases, workflows, context manifests,
// cache outcomes, evidence records and maturity export records.
// It is safe for concurrent use.
//
// For Phase 1 this is local-process only.  Promotion to durable storage
// (SQLite/Postgres) is the path to team/organisation use.
type RegistryStore struct {
	mu            sync.RWMutex
	useCases      map[string]UseCase
	workflows     map[string]Workflow
	manifests     map[string]ContextManifest
	cacheOutcomes []CacheOutcome
	evidence      []EvidenceRecord
	exports       []MaturityExportRecord
}

func NewRegistryStore() *RegistryStore {
	return &RegistryStore{
		useCases:  make(map[string]UseCase),
		workflows: make(map[string]Workflow),
		manifests: make(map[string]ContextManifest),
	}
}

// RegistryStoreInterface is the shared interface between in-memory and durable registry stores.
// Both RegistryStore and DurableRegistryStore satisfy this interface.
type RegistryStoreInterface interface {
	RegisterUseCase(UseCase) error
	GetUseCase(string) (UseCase, bool)
	ListUseCases() ([]UseCase, error)
	RegisterWorkflow(Workflow) error
	GetWorkflow(string) (Workflow, bool)
	ListWorkflows() ([]Workflow, error)
	CreateManifest(ContextManifest) error
	GetManifest(string) (ContextManifest, bool)
	AppendCacheOutcome(CacheOutcome) error
	CacheOutcomes() ([]CacheOutcome, error)
	AppendEvidence(EvidenceRecord) error
	Evidence() ([]EvidenceRecord, error)
	AppendExport(MaturityExportRecord) error
	Exports() ([]MaturityExportRecord, error)
}

// UseCase methods.

func (s *RegistryStore) RegisterUseCase(uc UseCase) error {
	if uc.ID == "" {
		return errors.New("use_case id is required")
	}
	if uc.Owner == "" {
		return errors.New("use_case owner is required")
	}
	if uc.Domain == "" {
		return errors.New("use_case domain is required")
	}
	if uc.Classification == "" {
		return errors.New("use_case classification is required")
	}
	if uc.RiskLevel == "" {
		return errors.New("use_case risk_level is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if uc.CreatedAt.IsZero() {
		uc.CreatedAt = time.Now().UTC()
	}
	s.useCases[uc.ID] = uc
	return nil
}

func (s *RegistryStore) GetUseCase(id string) (UseCase, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	uc, ok := s.useCases[id]
	return uc, ok
}

func (s *RegistryStore) ListUseCases() ([]UseCase, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]UseCase, 0, len(s.useCases))
	for _, uc := range s.useCases {
		out = append(out, uc)
	}
	return out, nil
}

// Workflow methods.

func (s *RegistryStore) RegisterWorkflow(wf Workflow) error {
	if wf.ID == "" {
		return errors.New("workflow id is required")
	}
	if wf.Name == "" {
		return errors.New("workflow name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if wf.CreatedAt.IsZero() {
		wf.CreatedAt = time.Now().UTC()
	}
	s.workflows[wf.ID] = wf
	return nil
}

func (s *RegistryStore) GetWorkflow(id string) (Workflow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wf, ok := s.workflows[id]
	return wf, ok
}

func (s *RegistryStore) ListWorkflows() ([]Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Workflow, 0, len(s.workflows))
	for _, wf := range s.workflows {
		out = append(out, wf)
	}
	return out, nil
}

// ContextManifest methods.

func (s *RegistryStore) CreateManifest(m ContextManifest) error {
	if m.ID == "" {
		return errors.New("manifest id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	s.manifests[m.ID] = m
	return nil
}

func (s *RegistryStore) GetManifest(id string) (ContextManifest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.manifests[id]
	return m, ok
}

// CacheOutcome methods.

func (s *RegistryStore) AppendCacheOutcome(c CacheOutcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.RecordedAt.IsZero() {
		c.RecordedAt = time.Now().UTC()
	}
	s.cacheOutcomes = append(s.cacheOutcomes, c)
	return nil
}

func (s *RegistryStore) CacheOutcomes() ([]CacheOutcome, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]CacheOutcome, len(s.cacheOutcomes))
	copy(out, s.cacheOutcomes)
	return out, nil
}

// Evidence methods.

func (s *RegistryStore) AppendEvidence(e EvidenceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now().UTC()
	}
	s.evidence = append(s.evidence, e)
	return nil
}

func (s *RegistryStore) Evidence() ([]EvidenceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]EvidenceRecord, len(s.evidence))
	copy(out, s.evidence)
	return out, nil
}

// MaturityExport methods.

func (s *RegistryStore) AppendExport(r MaturityExportRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.RecordedAt.IsZero() {
		r.RecordedAt = time.Now().UTC()
	}
	s.exports = append(s.exports, r)
	return nil
}

func (s *RegistryStore) Exports() ([]MaturityExportRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MaturityExportRecord, len(s.exports))
	copy(out, s.exports)
	return out, nil
}

// RegistryHandler serves the control-plane registry APIs.
type RegistryHandler struct {
	store   RegistryStoreInterface
	service *SessionService
	metrics *MetricsHandler
	newID   func(prefix string) string
}

func NewRegistryHandler(store RegistryStoreInterface, service *SessionService) http.Handler {
	return &RegistryHandler{
		store:   store,
		service: service,
		newID:   randomID,
	}
}

func NewRegistryHandlerWithMetrics(store RegistryStoreInterface, service *SessionService, metrics *MetricsHandler) http.Handler {
	return &RegistryHandler{
		store:   store,
		service: service,
		metrics: metrics,
		newID:   randomID,
	}
}

func (h *RegistryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	authReq, ok := h.service.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	path := r.URL.Path
	switch {
	case path == "/v1/use-cases" && r.Method == http.MethodPost:
		h.createUseCase(w, r)
	case path == "/v1/use-cases" && r.Method == http.MethodGet:
		h.listUseCases(w, r)
	case strings.HasPrefix(path, "/v1/use-cases/") && r.Method == http.MethodGet:
		h.getUseCase(w, r)
	case path == "/v1/workflows" && r.Method == http.MethodPost:
		h.createWorkflow(w, r)
	case path == "/v1/workflows" && r.Method == http.MethodGet:
		h.listWorkflows(w, r)
	case path == "/v1/context-manifests" && r.Method == http.MethodPost:
		h.createManifest(w, r)
	case strings.HasPrefix(path, "/v1/context-manifests/") && r.Method == http.MethodGet:
		h.getManifest(w, r)
	case path == "/v1/reporting/maturity-governance" && r.Method == http.MethodGet:
		h.listMaturityExports(w, r)
	case path == "/v1/cache-outcomes" && r.Method == http.MethodPost:
		h.createCacheOutcome(w, r)
	case path == "/v1/cache-outcomes" && r.Method == http.MethodGet:
		h.listCacheOutcomes(w, r)
	case path == "/v1/evidence" && r.Method == http.MethodPost:
		h.createEvidence(w, r)
	case path == "/v1/evidence" && r.Method == http.MethodGet:
		h.listEvidence(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *RegistryHandler) createUseCase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID              string `json:"id"`
		Owner           string `json:"owner"`
		Domain          string `json:"domain"`
		ExpectedBenefit string `json:"expected_benefit"`
		LinkedWorkItem  string `json:"linked_work_item,omitempty"`
		Classification  string `json:"classification"`
		RiskLevel       string `json:"risk_level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	uc := UseCase{
		ID:              req.ID,
		Owner:           req.Owner,
		Domain:          req.Domain,
		ExpectedBenefit: req.ExpectedBenefit,
		LinkedWorkItem:  req.LinkedWorkItem,
		Classification:  req.Classification,
		RiskLevel:       req.RiskLevel,
	}
	if err := h.store.RegisterUseCase(uc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if stored, ok := h.store.GetUseCase(uc.ID); ok {
		uc = stored
	}
	if h.metrics != nil {
		h.metrics.RecordUseCaseRegistered()
	}
	writeJSON(w, http.StatusCreated, uc)
}

func (h *RegistryHandler) listUseCases(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListUseCases()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "list use cases failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"use_cases": items})
}

func (h *RegistryHandler) listWorkflows(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListWorkflows()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "list workflows failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": items})
}

func (h *RegistryHandler) getUseCase(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/use-cases/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "use_case id is required"})
		return
	}
	uc, ok := h.store.GetUseCase(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "use_case not found"})
		return
	}
	writeJSON(w, http.StatusOK, uc)
}

func (h *RegistryHandler) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Stages      []string `json:"stages,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	wf := Workflow{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		Stages:      req.Stages,
	}
	if err := h.store.RegisterWorkflow(wf); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if stored, ok := h.store.GetWorkflow(wf.ID); ok {
		wf = stored
	}
	if h.metrics != nil {
		h.metrics.RecordWorkflowRegistered()
	}
	writeJSON(w, http.StatusCreated, wf)
}

func (h *RegistryHandler) createManifest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID              string   `json:"id"`
		SessionID       string   `json:"session_id"`
		Summary         string   `json:"summary"`
		SourceSystem    string   `json:"source_system"`
		SourceObjectID  string   `json:"source_object_id"`
		SourcePath      string   `json:"source_path,omitempty"`
		Actor           string   `json:"actor"`
		AuthScope       string   `json:"auth_scope"`
		Freshness       string   `json:"freshness,omitempty"`
		Classification  string   `json:"classification"`
		CacheStatus     string   `json:"cache_status"`
		ChunkHashes     []string `json:"chunk_hashes,omitempty"`
		InfluencedModel bool     `json:"influenced_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if !h.requireOwnedSession(w, r, req.SessionID) {
		return
	}
	m := ContextManifest{
		ID:              req.ID,
		SessionID:       req.SessionID,
		Summary:         req.Summary,
		SourceSystem:    req.SourceSystem,
		SourceObjectID:  req.SourceObjectID,
		SourcePath:      req.SourcePath,
		Actor:           req.Actor,
		AuthScope:       req.AuthScope,
		FetchedAt:       time.Now().UTC(),
		Freshness:       req.Freshness,
		Classification:  req.Classification,
		CacheStatus:     req.CacheStatus,
		ChunkHashes:     req.ChunkHashes,
		InfluencedModel: req.InfluencedModel,
	}
	if err := h.store.CreateManifest(m); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if stored, ok := h.store.GetManifest(m.ID); ok {
		m = stored
	}
	if h.metrics != nil {
		h.metrics.RecordManifestCreated()
	}
	writeJSON(w, http.StatusCreated, m)
}

func (h *RegistryHandler) getManifest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/context-manifests/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "manifest id is required"})
		return
	}
	m, ok := h.store.GetManifest(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "manifest not found"})
		return
	}
	if !h.requireOwnedSession(w, r, m.SessionID) {
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *RegistryHandler) listMaturityExports(w http.ResponseWriter, r *http.Request) {
	exports, err := h.store.Exports()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	filtered, ok := h.filterOwnedMaturityExports(w, r, exports)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exports": filtered,
		"count":   len(filtered),
	})
}

func (h *RegistryHandler) requireOwnedSession(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if h.service == nil || h.service.sessions == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return false
	}
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id is required"})
		return false
	}
	record, err := h.service.sessions.Get(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
		return false
	}
	actor := actorFromContext(r.Context())
	if record.ActorSubject != actor && actor != AdminOperatorSubject {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
		return false
	}
	return true
}

func (h *RegistryHandler) sessionOwnedByActor(ctx context.Context, sessionID string, actor string) (bool, error) {
	// The admin operator sees every actor's records for governance oversight.
	if actor == AdminOperatorSubject {
		return true, nil
	}
	if h.service == nil || h.service.sessions == nil {
		return false, errors.New("session store unavailable")
	}
	if sessionID == "" {
		return false, nil
	}
	record, err := h.service.sessions.Get(ctx, sessionID)
	if err != nil {
		return false, nil
	}
	return record.ActorSubject == actor, nil
}

func (h *RegistryHandler) filterOwnedCacheOutcomes(w http.ResponseWriter, r *http.Request, outcomes []CacheOutcome) ([]CacheOutcome, bool) {
	if h.service == nil || h.service.sessions == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return nil, false
	}
	actor := actorFromContext(r.Context())
	filtered := make([]CacheOutcome, 0, len(outcomes))
	for _, outcome := range outcomes {
		owned, err := h.sessionOwnedByActor(r.Context(), outcome.SessionID, actor)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
			return nil, false
		}
		if owned {
			filtered = append(filtered, outcome)
		}
	}
	return filtered, true
}

func (h *RegistryHandler) filterOwnedEvidence(w http.ResponseWriter, r *http.Request, evidence []EvidenceRecord) ([]EvidenceRecord, bool) {
	if h.service == nil || h.service.sessions == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return nil, false
	}
	actor := actorFromContext(r.Context())
	filtered := make([]EvidenceRecord, 0, len(evidence))
	for _, record := range evidence {
		owned, err := h.sessionOwnedByActor(r.Context(), record.SessionID, actor)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
			return nil, false
		}
		if owned {
			filtered = append(filtered, record)
		}
	}
	return filtered, true
}

func (h *RegistryHandler) filterOwnedMaturityExports(w http.ResponseWriter, r *http.Request, exports []MaturityExportRecord) ([]MaturityExportRecord, bool) {
	if h.service == nil || h.service.sessions == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return nil, false
	}
	actor := actorFromContext(r.Context())
	filtered := make([]MaturityExportRecord, 0, len(exports))
	for _, record := range exports {
		owned, err := h.sessionOwnedByActor(r.Context(), record.SessionID, actor)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
			return nil, false
		}
		if owned {
			filtered = append(filtered, record)
		}
	}
	return filtered, true
}

func (h *RegistryHandler) createCacheOutcome(w http.ResponseWriter, r *http.Request) {
	var c CacheOutcome
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if c.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id is required"})
		return
	}
	if !h.requireOwnedSession(w, r, c.SessionID) {
		return
	}
	if c.CacheKeyHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "cache_key_hash is required"})
		return
	}
	if c.ID == "" {
		c.ID = h.newID("cache")
	}
	if c.RecordedAt.IsZero() {
		c.RecordedAt = time.Now().UTC()
	}
	if err := h.store.AppendCacheOutcome(c); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if h.metrics != nil {
		if c.Hit {
			h.metrics.RecordCacheHit()
		} else {
			h.metrics.RecordCacheMiss()
		}
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *RegistryHandler) listCacheOutcomes(w http.ResponseWriter, r *http.Request) {
	outcomes, err := h.store.CacheOutcomes()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	filtered, ok := h.filterOwnedCacheOutcomes(w, r, outcomes)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"outcomes": filtered,
		"count":    len(filtered),
	})
}

func (h *RegistryHandler) createEvidence(w http.ResponseWriter, r *http.Request) {
	var e EvidenceRecord
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if e.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id is required"})
		return
	}
	if !h.requireOwnedSession(w, r, e.SessionID) {
		return
	}
	if e.EvidenceType == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "evidence_type is required"})
		return
	}
	if e.ID == "" {
		e.ID = h.newID("evidence")
	}
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now().UTC()
	}
	trust := h.evidenceTrustMetadata(e, r)
	e.TrustLevel = trust.TrustLevel
	e.EnforcementMode = trust.EnforcementMode
	if err := h.store.AppendEvidence(e); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if h.metrics != nil {
		h.metrics.RecordEvidenceRecorded()
	}
	writeJSON(w, http.StatusCreated, e)
}

func (h *RegistryHandler) evidenceTrustMetadata(e EvidenceRecord, r *http.Request) requestTrustMetadata {
	switch strings.ToLower(strings.TrimSpace(e.EvidenceType)) {
	case "external_tool_call", "external_model_call":
		return requestTrustMetadata{TrustLevel: "self_reported", EnforcementMode: "advisory"}
	default:
		return h.service.trustMetadataFromRequest(r)
	}
}

func (h *RegistryHandler) listEvidence(w http.ResponseWriter, r *http.Request) {
	ev, err := h.store.Evidence()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	filtered, ok := h.filterOwnedEvidence(w, r, ev)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"evidence": filtered,
		"count":    len(filtered),
	})
}
