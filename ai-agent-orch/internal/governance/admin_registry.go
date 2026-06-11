package governance

import (
	"net/http"

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
	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	switch r.URL.Path {
	case "/v1/admin/evidence":
		h.listEvidence(w, r)
	case "/v1/admin/cache-outcomes":
		h.listCacheOutcomes(w, r)
	case "/v1/admin/reporting/maturity-governance":
		h.listMaturityExports(w, r)
	default:
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	}
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
