package governance

import (
	"net/http"
	"sync/atomic"
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
}

func NewMetricsHandler() *MetricsHandler {
	return &MetricsHandler{}
}

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sessions_created":       h.sessionsCreated.Load(),
		"sessions_denied":        h.sessionsDenied.Load(),
		"patches_applied":        h.patchesApplied.Load(),
		"patches_rejected":       h.patchesRejected.Load(),
		"secrets_blocked":        h.secretsBlocked.Load(),
		"classification_blocked": h.classificationBlocked.Load(),
		"cost_capped":            h.costCapped.Load(),
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
