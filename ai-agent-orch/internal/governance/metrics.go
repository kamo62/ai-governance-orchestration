package governance

import (
	"net/http"
	"sync/atomic"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// MetricsHandler serves GET /metrics with basic Prometheus-compatible counters.
type MetricsHandler struct {
	sessionsCreated       atomic.Uint64
	sessionsDenied        atomic.Uint64
	patchesApplied        atomic.Uint64
	patchesRejected       atomic.Uint64
	secretsBlocked        atomic.Uint64
	classificationBlocked atomic.Uint64
	costCapped            atomic.Uint64
	useCasesRegistered    atomic.Uint64
	workflowsRegistered   atomic.Uint64
	manifestsCreated      atomic.Uint64
	cacheHits             atomic.Uint64
	cacheMisses           atomic.Uint64
	evidenceRecorded      atomic.Uint64
	exportsGenerated      atomic.Uint64
}

func NewMetricsHandler() *MetricsHandler {
	return &MetricsHandler{}
}

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"sessions_created":       h.sessionsCreated.Load(),
		"sessions_denied":        h.sessionsDenied.Load(),
		"patches_applied":        h.patchesApplied.Load(),
		"patches_rejected":       h.patchesRejected.Load(),
		"secrets_blocked":        h.secretsBlocked.Load(),
		"classification_blocked": h.classificationBlocked.Load(),
		"cost_capped":            h.costCapped.Load(),
		"use_cases_registered":   h.useCasesRegistered.Load(),
		"workflows_registered":   h.workflowsRegistered.Load(),
		"manifests_created":      h.manifestsCreated.Load(),
		"cache_hits":             h.cacheHits.Load(),
		"cache_misses":           h.cacheMisses.Load(),
		"evidence_recorded":      h.evidenceRecorded.Load(),
		"exports_generated":      h.exportsGenerated.Load(),
	})
}

func (h *MetricsHandler) RecordSessionCreated() {
	h.sessionsCreated.Add(1)
}

func (h *MetricsHandler) RecordSessionDenied() {
	h.sessionsDenied.Add(1)
}

func (h *MetricsHandler) RecordSecretBlocked() {
	h.secretsBlocked.Add(1)
}

func (h *MetricsHandler) RecordClassificationBlocked() {
	h.classificationBlocked.Add(1)
}

func (h *MetricsHandler) RecordCostCapped() {
	h.costCapped.Add(1)
}

func (h *MetricsHandler) RecordPatchApplied() {
	h.patchesApplied.Add(1)
}

func (h *MetricsHandler) RecordPatchRejected() {
	h.patchesRejected.Add(1)
}

func (h *MetricsHandler) RecordUseCaseRegistered() {
	h.useCasesRegistered.Add(1)
}

func (h *MetricsHandler) RecordWorkflowRegistered() {
	h.workflowsRegistered.Add(1)
}

func (h *MetricsHandler) RecordManifestCreated() {
	h.manifestsCreated.Add(1)
}

func (h *MetricsHandler) RecordCacheHit() {
	h.cacheHits.Add(1)
}

func (h *MetricsHandler) RecordCacheMiss() {
	h.cacheMisses.Add(1)
}

func (h *MetricsHandler) RecordEvidenceRecorded() {
	h.evidenceRecorded.Add(1)
}

func (h *MetricsHandler) RecordExportGenerated() {
	h.exportsGenerated.Add(1)
}
