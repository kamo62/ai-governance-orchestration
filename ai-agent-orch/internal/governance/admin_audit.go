package governance

import (
	"context"
	"net/http"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// AdminAuditHandler serves audit administration endpoints.
type AdminAuditHandler struct {
	audit   audit.Store
	service *SessionService
}

func NewAdminAuditHandler(auditStore audit.Store, service *SessionService) http.Handler {
	return &AdminAuditHandler{audit: auditStore, service: service}
}

func (h *AdminAuditHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.audit == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "audit store unavailable"})
		return
	}
	if h.service == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	if !h.service.RequireAdminRequest(w, r) {
		return
	}

	switch r.Method {
	case http.MethodPost:
		if r.URL.Path == "/v1/admin/audit/retention" {
			h.applyRetention(w, r)
			return
		}
	}
	httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
}

func (h *AdminAuditHandler) applyRetention(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MaxAgeHours int `json:"max_age_hours"`
	}
	if err := readJSON(w, r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.MaxAgeHours <= 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "max_age_hours must be > 0"})
		return
	}

	// Only SQLiteStore supports retention; FileStore is append-only.
	type retentionCapability interface {
		SupportsRetentionPolicy() bool
	}
	if capable, ok := h.audit.(retentionCapability); ok && !capable.SupportsRetentionPolicy() {
		httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"error": "retention not supported by current audit backend"})
		return
	}
	type retentionPurger interface {
		RetentionPolicy(context.Context, time.Duration) (int64, error)
	}
	purger, ok := h.audit.(retentionPurger)
	if !ok {
		httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"error": "retention not supported by current audit backend"})
		return
	}

	n, err := purger.RetentionPolicy(r.Context(), time.Duration(req.MaxAgeHours)*time.Hour)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "retention failed: " + err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"purged":        n,
		"max_age_hours": req.MaxAgeHours,
	})
}
