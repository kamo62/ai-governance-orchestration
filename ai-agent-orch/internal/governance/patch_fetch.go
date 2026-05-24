package governance

import (
	"net/http"
	"strings"
)

// PatchFetchHandler serves GET /v1/sessions/{id}/patches/{patch_id}.
type PatchFetchHandler struct {
	service *SessionService
}

func NewPatchFetchHandler(service *SessionService) http.Handler {
	return &PatchFetchHandler{service: service}
}

func (h *PatchFetchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.service == nil || h.service.patchBuffer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "patch buffer unavailable"})
		return
	}
	authReq, ok := h.service.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	sessionID, patchID := extractPatchFetchIDs(r.URL.Path)
	if sessionID == "" || patchID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID and patch ID are required"})
		return
	}

	actor := actorFromContext(r.Context())
	if h.service.sessions != nil {
		record, err := h.service.sessions.Get(r.Context(), sessionID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return
		}
		if record.ActorSubject != actor {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
			return
		}
	}
	if !h.service.patchKnown(sessionID, patchID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "patch not found"})
		return
	}

	payload, err := h.service.patchBuffer.Get(r.Context(), sessionID, patchID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "patch not found"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(payload))
}

func extractPatchFetchIDs(path string) (sessionID string, patchID string) {
	const prefix = "/v1/sessions/"
	if !strings.HasPrefix(path, prefix) {
		return "", ""
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/patches/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	if strings.Contains(parts[0], "/") || strings.Contains(parts[1], "/") {
		return "", ""
	}
	return parts[0], parts[1]
}
