package governance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"ai-agent-orch/internal/audit"
)

// PatchDecisionHandler serves POST /v1/sessions/{id}/patch-decision.
type PatchDecisionHandler struct {
	service *SessionService
	newID   func(prefix string) string
}

func NewPatchDecisionHandler(service *SessionService) http.Handler {
	return &PatchDecisionHandler{
		service: service,
		newID:   randomID,
	}
}

func (h *PatchDecisionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.service == nil || h.service.audit == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	if !h.service.RequireAuthorizedRequest(w, r) {
		return
	}

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/patch-decision")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	var req struct {
		PatchID  string `json:"patch_id"`
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.PatchID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "patch_id is required"})
		return
	}
	if req.Decision == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "decision is required"})
		return
	}
	validDecisions := map[string]struct{}{"applied": {}, "partially_applied": {}, "rejected": {}}
	if _, ok := validDecisions[req.Decision]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("invalid decision %q", req.Decision)})
		return
	}
	if !h.service.patchKnown(sessionID, req.PatchID) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "patch is not known for session"})
		return
	}

	eventID := h.newID("evt")
	_, err := h.service.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		SessionID:          sessionID,
		EventType:          "patch.decision",
		Actor:              "local-dev",
		Reason:             fmt.Sprintf("%s: %s", req.Decision, req.Reason),
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}
	if h.service.metrics != nil {
		switch req.Decision {
		case "applied", "partially_applied":
			h.service.metrics.RecordPatchApplied()
		case "rejected":
			h.service.metrics.RecordPatchRejected()
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":     sessionID,
		"patch_id":       req.PatchID,
		"decision":       req.Decision,
		"audit_event_id": eventID,
	})
}

func extractPatchID(payload string) string {
	var patch struct {
		PatchIDCamel string `json:"patchId"`
		PatchIDSnake string `json:"patch_id"`
	}
	if err := json.Unmarshal([]byte(payload), &patch); err != nil {
		return ""
	}
	if patch.PatchIDCamel != "" {
		return patch.PatchIDCamel
	}
	return patch.PatchIDSnake
}
