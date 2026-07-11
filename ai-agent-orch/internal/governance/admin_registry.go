package governance

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// NewAdminRegistryHandler serves cross-actor registry and reporting views for
// governance operators. The ordinary registry endpoints remain actor-scoped.
func NewAdminRegistryHandler(store RegistryStoreInterface, service *SessionService) http.Handler {
	return &AdminRegistryHandler{store: store, service: service}
}

type AdminRegistryHandler struct {
	store   RegistryStoreInterface
	service *SessionService
}

func (h *AdminRegistryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "registry store unavailable"})
		return
	}
	if h.service == nil || !h.service.RequireAdminRequest(w, r) {
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/evidence":
		h.listEvidence(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/cache-outcomes":
		h.listCacheOutcomes(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/reporting/maturity-governance":
		h.listMaturityExports(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/admin/evidence/"):
		h.decideEvidence(w, r)
	case r.URL.Path == "/v1/admin/evidence" ||
		r.URL.Path == "/v1/admin/cache-outcomes" ||
		r.URL.Path == "/v1/admin/reporting/maturity-governance" ||
		strings.HasPrefix(r.URL.Path, "/v1/admin/evidence/"):
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	default:
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
}

func (h *AdminRegistryHandler) decideEvidence(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/admin/evidence/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "confirm" && parts[1] != "reject") {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeRequestBodyError(w, err)
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "reason is required"})
		return
	}
	status := "confirmed"
	if parts[1] == "reject" {
		status = "rejected"
	}
	decision := &EvidenceDecision{
		ID:          randomID("evidence_decision"),
		EvidenceID:  parts[0],
		ConfirmedBy: AdminOperatorSubject,
		Decision:    status,
		Reason:      body.Reason,
		RecordedAt:  time.Now().UTC(),
	}
	evidence, err := h.store.TransitionEvidence(parts[0], status, decision)
	if err != nil {
		switch {
		case errors.Is(err, ErrEvidenceNotFound):
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		case errors.Is(err, ErrEvidenceTransitionConflict):
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		default:
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "evidence decision failed"})
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"evidence": evidence, "decision": decision})
}

func (h *AdminRegistryHandler) listEvidence(w http.ResponseWriter, _ *http.Request) {
	evidence, err := h.store.Evidence()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "list evidence failed"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"evidence": evidence, "count": len(evidence)})
}

func (h *AdminRegistryHandler) listCacheOutcomes(w http.ResponseWriter, _ *http.Request) {
	outcomes, err := h.store.CacheOutcomes()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "list cache outcomes failed"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"outcomes": outcomes, "count": len(outcomes)})
}

func (h *AdminRegistryHandler) listMaturityExports(w http.ResponseWriter, _ *http.Request) {
	exports, err := h.store.Exports()
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "list maturity exports failed"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"exports": exports, "count": len(exports)})
}
