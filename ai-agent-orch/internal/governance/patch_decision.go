package governance

import (
	"encoding/json"
	"fmt"
	"log"
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
	authReq, ok := h.service.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/patch-decision")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	// Enforce session ownership.
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

	var req struct {
		PatchID  string `json:"patch_id"`
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
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
	trust := h.service.trustMetadataFromRequest(r)
	_, err := h.service.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		ParentEventID:      h.service.parentEventID(sessionID),
		SessionID:          sessionID,
		EventType:          "patch.decision",
		Actor:              actor,
		Reason:             fmt.Sprintf("%s: %s", req.Decision, req.Reason),
		PatchID:            req.PatchID,
		PatchDecision:      req.Decision,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         trust.TrustLevel,
		EnforcementMode:    trust.EnforcementMode,
	})
	if err != nil {
		// Surface the append failure cause: a hash-chain conflict almost always
		// means a stale local audit volume from an older stack.
		log.Printf("patch decision audit append failed for session %s: %v", sessionID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "audit write failed",
			"hint":  audit.FailureHint(err),
		})
		return
	}
	h.service.rememberEventID(sessionID, eventID)
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
