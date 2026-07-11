package governance

import (
	"net/http"
	"sync"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// maturitySnapshotEventType marks MaturityExportRecord rows produced by the
// explicit admin-triggered snapshot (as opposed to any future live,
// per-lifecycle-event export writer).
const maturitySnapshotEventType = "session.maturity_snapshot"

// MaturityExportRunConfig configures the admin-triggered maturity snapshot.
// Like InsightHandlerConfig, session/audit/policy-decision access goes
// through Service; Registry is passed explicitly.
type MaturityExportRunConfig struct {
	Service  *SessionService
	Registry RegistryStoreInterface
	// Now overrides the clock for deterministic tests. Nil means time.Now().
	Now func() time.Time
}

type maturityExportRunHandler struct {
	cfg MaturityExportRunConfig
	// This shell is single-process/single-writer, so a process-local mutex is sufficient.
	mu sync.Mutex
}

// MaturityExportRunResponse reports what the snapshot run did: how many
// sessions were newly projected into maturity_exports versus skipped because
// they were already exported for the same window end date.
type MaturityExportRunResponse struct {
	Window           InsightWindow `json:"window"`
	Appended         int           `json:"appended"`
	Skipped          int           `json:"skipped"`
	AppendedSessions []string      `json:"appended_sessions,omitempty"`
	SkippedSessions  []string      `json:"skipped_sessions,omitempty"`
}

// NewMaturityExportRunHandler serves POST /v1/admin/reporting/maturity-export/run.
// It is the sole writer of MaturityExportRecord rows: an explicit,
// admin-triggered materialization boundary. It must never be invoked from a
// GET handler.
func NewMaturityExportRunHandler(cfg MaturityExportRunConfig) http.Handler {
	return &maturityExportRunHandler{cfg: cfg}
}

func (h *maturityExportRunHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.cfg.Service == nil || h.cfg.Registry == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "maturity export unavailable"})
		return
	}
	if !h.cfg.Service.RequireAdminRequest(w, r) {
		return
	}
	if h.cfg.Service.sessions == nil || h.cfg.Service.auditReader == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session or audit store unavailable"})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now().UTC()
	if h.cfg.Now != nil {
		now = h.cfg.Now().UTC()
	}
	since, err := parseInsightWindow(r.URL.Query().Get("window"), now)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	lister, ok := h.cfg.Service.sessions.(sessionExportLister)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"error": "session store does not support windowed export"})
		return
	}
	sessions, err := lister.ListAllSince(r.Context(), since, insightSessionCap)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session lookup failed"})
		return
	}

	// Idempotency: skip sessions already exported by this snapshot for the
	// same window end date, so re-running the same day's snapshot does not
	// duplicate rows.
	existingExports, err := h.cfg.Registry.Exports()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "export lookup failed"})
		return
	}
	windowEndDate := now.Format("2006-01-02")
	alreadyExported := make(map[string]struct{}, len(existingExports))
	for _, export := range existingExports {
		if export.EventType != maturitySnapshotEventType {
			continue
		}
		if export.RecordedAt.UTC().Format("2006-01-02") != windowEndDate {
			continue
		}
		alreadyExported[export.SessionID] = struct{}{}
	}

	allEvidence, err := h.cfg.Registry.Evidence()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "evidence lookup failed"})
		return
	}
	evidenceBySession := make(map[string][]EvidenceRecord, len(sessions))
	for _, record := range allEvidence {
		evidenceBySession[record.SessionID] = append(evidenceBySession[record.SessionID], record)
	}

	var policyDecisions []PolicyDecisionRecord
	if h.cfg.Service.policyDecisions != nil {
		all, err := h.cfg.Service.policyDecisions.ListPolicyDecisions(r.Context(), "")
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "policy decision lookup failed"})
			return
		}
		policyDecisions = all
	}
	decisionsBySession := make(map[string][]PolicyDecisionRecord, len(sessions))
	for _, decision := range policyDecisions {
		if decision.SessionID == "" {
			continue
		}
		decisionsBySession[decision.SessionID] = append(decisionsBySession[decision.SessionID], decision)
	}

	response := MaturityExportRunResponse{Window: InsightWindow{Since: since, Until: now}}
	for _, session := range sessions {
		if _, skip := alreadyExported[session.SessionID]; skip {
			response.Skipped++
			response.SkippedSessions = append(response.SkippedSessions, session.SessionID)
			continue
		}
		events, err := h.cfg.Service.auditReader.EventsBySession(r.Context(), session.SessionID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit lookup failed"})
			return
		}
		usage := SummarizeSessionUsageWithPricing(r.Context(), events, h.cfg.Service.modelPricing)
		record := buildMaturitySnapshot(session, events, decisionsBySession[session.SessionID], evidenceBySession[session.SessionID], usage, now)
		if err := h.cfg.Registry.AppendExport(record); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "export append failed"})
			return
		}
		response.Appended++
		response.AppendedSessions = append(response.AppendedSessions, session.SessionID)
	}

	httpx.WriteJSON(w, http.StatusOK, response)
}

// buildMaturitySnapshot projects one session's already-stored facts into a
// MaturityExportRecord. It performs no I/O: every input is pre-fetched by
// the caller.
func buildMaturitySnapshot(session SessionRecord, events []audit.Event, decisions []PolicyDecisionRecord, evidence []EvidenceRecord, usage SessionUsageSummary, now time.Time) MaturityExportRecord {
	record := MaturityExportRecord{
		SessionID:            session.SessionID,
		EventType:            maturitySnapshotEventType,
		Actor:                session.ActorSubject,
		UseCaseID:            session.UseCaseID,
		WorkflowID:           session.WorkflowID,
		WorkItemID:           session.WorkItemID,
		Repository:           session.RepoURL,
		Branch:               session.Branch,
		Commit:               session.CommitSHA,
		Classification:       session.Classification,
		Agent:                session.Agent,
		FinalStatus:          session.Status,
		BaselineCostUSD:      session.BaselineCostUSD,
		ToolCostUSD:          session.ToolCostUSD,
		PlatformCostUSD:      session.PlatformCostUSD,
		ReviewCostUSD:        session.ReviewCostUSD,
		VerificationCostUSD:  session.VerificationCostUSD,
		RetryCount:           session.RetryCount,
		ModelCostUSD:         usage.EstimatedCostUSD,
		EvidenceCompleteness: evidenceCompleteness(evidence),
		RecordedAt:           now,
	}

	if len(decisions) > 0 {
		latest := decisions[0]
		for _, decision := range decisions[1:] {
			if decision.RecordedAt.After(latest.RecordedAt) {
				latest = decision
			}
		}
		if latest.Allowed {
			record.PolicyDecision = "allowed"
		} else {
			record.PolicyDecision = "denied"
		}
		record.PolicyReason = latest.Reason
		record.CostCapResult = costCapResultFromDecisions(decisions)
	}

	if patchDecision := latestPatchDecision(events); patchDecision != "" {
		record.PatchDecision = patchDecision
	}

	return record
}

// costCapResultFromDecisions summarizes a session's cost-cap posture across
// its policy decisions: "exceeded" if any denial cited the cost cap,
// "within_cap" if the cap was active but never tripped, "" if the cap was
// never evaluated for this session.
func costCapResultFromDecisions(decisions []PolicyDecisionRecord) string {
	exceeded := false
	capped := false
	for _, decision := range decisions {
		if !decision.Allowed && isCostCapReason(decision.Reason) {
			exceeded = true
		}
		if decision.CostCapUSD > 0 {
			capped = true
		}
	}
	if exceeded {
		return "exceeded"
	}
	if capped {
		return "within_cap"
	}
	return ""
}

// latestPatchDecision returns the most recent patch.decision audit event's
// decision (applied, partially_applied, rejected) for a session, or "" if
// none was recorded.
func latestPatchDecision(events []audit.Event) string {
	var latest audit.Event
	found := false
	for _, event := range events {
		if event.EventType != "patch.decision" {
			continue
		}
		if !found || event.RecordedAt.After(latest.RecordedAt) {
			latest = event
			found = true
		}
	}
	if !found {
		return ""
	}
	return latest.PatchDecision
}

// evidenceCompleteness is the fraction of a session's evidence records whose
// status is confirmed or candidate. Zero evidence records yields 0.
func evidenceCompleteness(evidence []EvidenceRecord) float64 {
	if len(evidence) == 0 {
		return 0
	}
	complete := 0
	for _, record := range evidence {
		if record.Status == "confirmed" || record.Status == "candidate" {
			complete++
		}
	}
	return float64(complete) / float64(len(evidence))
}
