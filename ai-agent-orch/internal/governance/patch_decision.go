package governance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/logx"
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
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.service == nil || h.service.audit == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	authReq, ok := h.service.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/patch-decision")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	// Enforce session ownership.
	actor := actorFromContext(r.Context())
	if h.service.sessions != nil {
		record, err := h.service.sessions.Get(r.Context(), sessionID)
		if err != nil {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return
		}
		if record.ActorSubject != actor {
			httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
			return
		}
	}

	var req struct {
		PatchID  string `json:"patch_id"`
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}
	if err := readJSON(w, r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.PatchID == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "patch_id is required"})
		return
	}
	if req.Decision == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "decision is required"})
		return
	}
	validDecisions := map[string]struct{}{"applied": {}, "partially_applied": {}, "rejected": {}}
	if _, ok := validDecisions[req.Decision]; !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("invalid decision %q", req.Decision)})
		return
	}
	if !h.service.patchKnown(sessionID, req.PatchID) {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "patch is not known for session"})
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
		logx.Errorf("patch decision audit append failed for session %s: %v", sessionID, err)
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
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

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
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
