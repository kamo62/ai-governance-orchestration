package governance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/confidence"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
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
	ID                string          `json:"id"`
	SessionID         string          `json:"session_id"`
	EvidenceType      string          `json:"evidence_type"`
	Description       string          `json:"description"`
	TestResult        string          `json:"test_result,omitempty"`
	QualitySystemLink string          `json:"quality_system_link,omitempty"`
	SecurityFinding   string          `json:"security_finding,omitempty"`
	ApprovalReceipt   string          `json:"approval_receipt,omitempty"`
	PatchDecision     string          `json:"patch_decision,omitempty"`
	ExternalTicket    string          `json:"external_ticket,omitempty"`
	TrustLevel        string          `json:"trust_level,omitempty"`
	EnforcementMode   string          `json:"enforcement_mode,omitempty"`
	SubjectKey        string          `json:"subject_key"`
	Confidence        *float64        `json:"confidence,omitempty"`
	ConfidenceBand    confidence.Band `json:"confidence_band,omitempty"`
	SourceAuthority   int             `json:"source_authority"`
	EvidenceStrength  string          `json:"evidence_strength"`
	Status            string          `json:"status"`
	// ClientEventID is an optional client-supplied idempotency key. Retried
	// posts that reuse the same (session_id, client_event_id) pair are
	// deduped server-side instead of creating a second record.
	ClientEventID string    `json:"client_event_id,omitempty"`
	RecordedAt    time.Time `json:"recorded_at"`
}

// EvidenceDecision is an immutable administrative decision attached to evidence.
type EvidenceDecision struct {
	ID          string    `json:"confirmation_id"`
	EvidenceID  string    `json:"evidence_id"`
	ConfirmedBy string    `json:"confirmed_by"`
	Decision    string    `json:"decision"`
	Reason      string    `json:"reason"`
	RecordedAt  time.Time `json:"recorded_at"`
}

var (
	ErrEvidenceNotFound           = errors.New("evidence not found")
	ErrEvidenceTransitionConflict = errors.New("evidence status transition conflict")
)

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
	mu                sync.RWMutex
	useCases          map[string]UseCase
	workflows         map[string]Workflow
	manifests         map[string]ContextManifest
	cacheOutcomes     []CacheOutcome
	evidence          []EvidenceRecord
	evidenceDecisions []EvidenceDecision
	exports           []MaturityExportRecord
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
	// AppendEvidence stores e, unless e.ClientEventID is non-empty and a
	// record with the same (session_id, client_event_id) already exists, in
	// which case the existing record is returned unchanged with duplicate
	// set to true instead of an error.
	AppendEvidence(e EvidenceRecord) (stored EvidenceRecord, duplicate bool, err error)
	Evidence() ([]EvidenceRecord, error)
	TransitionEvidence(string, string, *EvidenceDecision) (EvidenceRecord, error)
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

func (s *RegistryStore) AppendEvidence(e EvidenceRecord) (EvidenceRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e = evidenceWithDefaults(e)
	e.ConfidenceBand = ""
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now().UTC()
	}
	clientEventID := strings.TrimSpace(e.ClientEventID)
	if clientEventID != "" {
		for _, existing := range s.evidence {
			if existing.SessionID == e.SessionID && strings.TrimSpace(existing.ClientEventID) == clientEventID {
				return existing, true, nil
			}
		}
	}
	s.evidence = append(s.evidence, e)
	return e, false, nil
}

func (s *RegistryStore) Evidence() ([]EvidenceRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]EvidenceRecord, len(s.evidence))
	copy(out, s.evidence)
	for i := range out {
		out[i] = evidenceWithConfidenceBand(out[i])
	}
	return out, nil
}

func (s *RegistryStore) TransitionEvidence(id string, to string, decision *EvidenceDecision) (EvidenceRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.evidence {
		if s.evidence[i].ID != id {
			continue
		}
		if err := validateEvidenceTransition(s.evidence[i].Status, to, decision); err != nil {
			return EvidenceRecord{}, err
		}
		if decision != nil && decision.EvidenceID != id {
			return EvidenceRecord{}, errors.New("evidence decision evidence_id mismatch")
		}
		s.evidence[i].Status = to
		if decision != nil {
			s.evidence[i].SourceAuthority = 2
			s.evidenceDecisions = append(s.evidenceDecisions, *decision)
		}
		return evidenceWithConfidenceBand(s.evidence[i]), nil
	}
	return EvidenceRecord{}, ErrEvidenceNotFound
}

func evidenceWithDefaults(e EvidenceRecord) EvidenceRecord {
	if e.Status == "" {
		e.Status = "legacy"
	}
	if e.EvidenceStrength == "" {
		e.EvidenceStrength = "weak"
	}
	if e.SourceAuthority == 0 {
		switch e.TrustLevel {
		case "gateway_enforced":
			e.SourceAuthority = 1
		case "managed_client":
			e.SourceAuthority = 3
		default:
			e.SourceAuthority = 4
		}
	}
	return e
}

func evidenceWithConfidenceBand(e EvidenceRecord) EvidenceRecord {
	e.ConfidenceBand = ""
	if e.Confidence != nil {
		e.ConfidenceBand = confidence.ToBand(*e.Confidence)
	}
	return e
}

func validateEvidenceTransition(from string, to string, decision *EvidenceDecision) error {
	allowed := to == "candidate" && from == "proposed" && decision == nil
	if to == "confirmed" {
		allowed = (from == "proposed" || from == "candidate" || from == "legacy") && decision != nil
	}
	if to == "rejected" {
		allowed = from != "rejected" && decision != nil
	}
	if !allowed {
		return fmt.Errorf("%w: %s -> %s", ErrEvidenceTransitionConflict, from, to)
	}
	if decision != nil && (decision.ID == "" || decision.Decision != to || strings.TrimSpace(decision.Reason) == "" || strings.TrimSpace(decision.ConfirmedBy) == "" || decision.RecordedAt.IsZero()) {
		return errors.New("evidence decision id, decision, reason, confirmed_by, and recorded_at are required")
	}
	return nil
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
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
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
	if err := readJSON(w, r, &req); err != nil {
		writeRequestBodyError(w, err)
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
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if stored, ok := h.store.GetUseCase(uc.ID); ok {
		uc = stored
	}
	if h.metrics != nil {
		h.metrics.RecordUseCaseRegistered()
	}
	httpx.WriteJSON(w, http.StatusCreated, uc)
}

func (h *RegistryHandler) listUseCases(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListUseCases()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "list use cases failed"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"use_cases": items})
}

func (h *RegistryHandler) listWorkflows(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.ListWorkflows()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "list workflows failed"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"workflows": items})
}

func (h *RegistryHandler) getUseCase(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/use-cases/")
	if id == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "use_case id is required"})
		return
	}
	uc, ok := h.store.GetUseCase(id)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "use_case not found"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, uc)
}

func (h *RegistryHandler) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Stages      []string `json:"stages,omitempty"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	wf := Workflow{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		Stages:      req.Stages,
	}
	if err := h.store.RegisterWorkflow(wf); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if stored, ok := h.store.GetWorkflow(wf.ID); ok {
		wf = stored
	}
	if h.metrics != nil {
		h.metrics.RecordWorkflowRegistered()
	}
	httpx.WriteJSON(w, http.StatusCreated, wf)
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
	if err := readJSON(w, r, &req); err != nil {
		writeRequestBodyError(w, err)
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
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if stored, ok := h.store.GetManifest(m.ID); ok {
		m = stored
	}
	if h.metrics != nil {
		h.metrics.RecordManifestCreated()
	}
	httpx.WriteJSON(w, http.StatusCreated, m)
}

func (h *RegistryHandler) getManifest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/context-manifests/")
	if id == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "manifest id is required"})
		return
	}
	m, ok := h.store.GetManifest(id)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "manifest not found"})
		return
	}
	if !h.requireOwnedSession(w, r, m.SessionID) {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

func (h *RegistryHandler) listMaturityExports(w http.ResponseWriter, r *http.Request) {
	exports, err := h.store.Exports()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	filtered, ok := h.filterOwnedMaturityExports(w, r, exports)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"exports": filtered,
		"count":   len(filtered),
	})
}

func (h *RegistryHandler) requireOwnedSession(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if h.service == nil || h.service.sessions == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return false
	}
	if sessionID == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id is required"})
		return false
	}
	record, err := h.service.sessions.Get(r.Context(), sessionID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
		return false
	}
	actor := actorFromContext(r.Context())
	if record.ActorSubject != actor && actor != AdminOperatorSubject {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
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
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return nil, false
	}
	actor := actorFromContext(r.Context())
	filtered := make([]CacheOutcome, 0, len(outcomes))
	for _, outcome := range outcomes {
		owned, err := h.sessionOwnedByActor(r.Context(), outcome.SessionID, actor)
		if err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
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
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return nil, false
	}
	actor := actorFromContext(r.Context())
	filtered := make([]EvidenceRecord, 0, len(evidence))
	for _, record := range evidence {
		owned, err := h.sessionOwnedByActor(r.Context(), record.SessionID, actor)
		if err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
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
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return nil, false
	}
	actor := actorFromContext(r.Context())
	filtered := make([]MaturityExportRecord, 0, len(exports))
	for _, record := range exports {
		owned, err := h.sessionOwnedByActor(r.Context(), record.SessionID, actor)
		if err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
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
	if err := readJSON(w, r, &c); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	if c.SessionID == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id is required"})
		return
	}
	if !h.requireOwnedSession(w, r, c.SessionID) {
		return
	}
	if c.CacheKeyHash == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "cache_key_hash is required"})
		return
	}
	if c.ID == "" {
		c.ID = h.newID("cache")
	}
	if c.RecordedAt.IsZero() {
		c.RecordedAt = time.Now().UTC()
	}
	if err := h.store.AppendCacheOutcome(c); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if h.metrics != nil {
		if c.Hit {
			h.metrics.RecordCacheHit()
		} else {
			h.metrics.RecordCacheMiss()
		}
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *RegistryHandler) listCacheOutcomes(w http.ResponseWriter, r *http.Request) {
	outcomes, err := h.store.CacheOutcomes()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	filtered, ok := h.filterOwnedCacheOutcomes(w, r, outcomes)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"outcomes": filtered,
		"count":    len(filtered),
	})
}

func (h *RegistryHandler) createEvidence(w http.ResponseWriter, r *http.Request) {
	var e EvidenceRecord
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err != nil {
		writeRequestBodyError(w, err)
		return
	}
	if err := json.Unmarshal(body, &e); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	var supplied map[string]json.RawMessage
	if err := json.Unmarshal(body, &supplied); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if _, ok := supplied["status"]; ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "status is server-derived and must not be supplied"})
		return
	}
	if _, ok := supplied["source_authority"]; ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "source_authority is server-derived and must not be supplied"})
		return
	}
	if e.SessionID == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id is required"})
		return
	}
	if !h.requireOwnedSession(w, r, e.SessionID) {
		return
	}
	if e.EvidenceType == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "evidence_type is required"})
		return
	}
	e.ClientEventID = strings.TrimSpace(e.ClientEventID)
	if e.ID == "" {
		e.ID = h.newID("evidence")
	}
	if e.RecordedAt.IsZero() {
		e.RecordedAt = time.Now().UTC()
	}
	trust := h.evidenceTrustMetadata(e, r)
	e.TrustLevel = trust.TrustLevel
	e.EnforcementMode = trust.EnforcementMode
	if err := deriveEvidenceProvenance(&e, trust); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	stored, duplicate, err := h.store.AppendEvidence(e)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if h.metrics != nil && !duplicate {
		h.metrics.RecordEvidenceRecorded()
	}
	httpx.WriteJSON(w, http.StatusCreated, evidenceCreateResponse{
		EvidenceRecord: evidenceWithConfidenceBand(stored),
		Duplicate:      duplicate,
	})
}

func writeRequestBodyError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	httpx.WriteJSON(w, status, map[string]any{"error": "invalid request body: " + err.Error()})
}

// evidenceCreateResponse is the POST /v1/evidence response shape: the normal
// evidence record, plus a duplicate marker set when the request replayed an
// already-recorded client_event_id instead of creating a new record.
type evidenceCreateResponse struct {
	EvidenceRecord
	Duplicate bool `json:"duplicate,omitempty"`
}

func (h *RegistryHandler) evidenceTrustMetadata(e EvidenceRecord, r *http.Request) requestTrustMetadata {
	switch strings.ToLower(strings.TrimSpace(e.EvidenceType)) {
	case "external_tool_call", "external_model_call":
		return requestTrustMetadata{TrustLevel: "self_reported", EnforcementMode: "advisory"}
	default:
		return h.service.trustMetadataFromRequest(r)
	}
}

func deriveEvidenceProvenance(e *EvidenceRecord, trust requestTrustMetadata) error {
	if e.Status != "" {
		return errors.New("status is server-derived and must not be supplied")
	}
	if e.SourceAuthority != 0 {
		return errors.New("source_authority is server-derived and must not be supplied")
	}
	if e.Confidence != nil && (*e.Confidence < 0 || *e.Confidence > 1) {
		return errors.New("confidence must be between 0 and 1")
	}

	defaultStrength := "weak"
	maxStrength := 1
	switch trust.TrustLevel {
	case "gateway_enforced":
		e.SourceAuthority = 1
		e.Status = "candidate"
		defaultStrength, maxStrength = "strong", 3
	case "managed_client":
		e.SourceAuthority = 3
		e.Status = "candidate"
		defaultStrength, maxStrength = "medium", 2
	default:
		e.SourceAuthority = 4
		e.Status = "proposed"
	}

	if e.EvidenceStrength == "" {
		e.EvidenceStrength = defaultStrength
		return nil
	}
	strength := map[string]int{"weak": 1, "medium": 2, "strong": 3}[e.EvidenceStrength]
	if strength == 0 {
		return errors.New("evidence_strength must be strong, medium, or weak")
	}
	if strength > maxStrength {
		return fmt.Errorf("evidence_strength %q exceeds the %s lane cap", e.EvidenceStrength, trust.TrustLevel)
	}
	return nil
}

func (h *RegistryHandler) listEvidence(w http.ResponseWriter, r *http.Request) {
	ev, err := h.store.Evidence()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	filtered, ok := h.filterOwnedEvidence(w, r, ev)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"evidence": filtered,
		"count":    len(filtered),
	})
}
