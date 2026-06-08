package governance

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"ai-agent-orch/internal/audit"
)

// ConfirmHandler serves POST /v1/sessions/{id}/confirm.
type ConfirmHandler struct {
	service *SessionService
	orch    OrchestratorClient
	events  *EventStore
	newID   func(prefix string) string
}

func NewConfirmHandler(service *SessionService, orch OrchestratorClient) http.Handler {
	return &ConfirmHandler{
		service: service,
		orch:    orch,
		newID:   randomID,
	}
}

func NewConfirmHandlerWithEvents(service *SessionService, orch OrchestratorClient, events *EventStore) http.Handler {
	return &ConfirmHandler{
		service: service,
		orch:    orch,
		events:  events,
		newID:   randomID,
	}
}

func (h *ConfirmHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/confirm")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	var req struct {
		Agent string `json:"agent"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.Agent == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent is required"})
		return
	}
	if blocked, reason := h.service.blockedByKillSwitch(req.Agent); blocked {
		if err := h.service.appendDenied(r.Context(), reason, nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		writeJSON(w, http.StatusLocked, map[string]any{"error": reason})
		return
	}

	var prompt string
	if h.events != nil {
		var ok bool
		prompt, ok = h.service.promptForSession(sessionID)
		if !ok {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "session prompt unavailable"})
			return
		}
	}

	// Enforce session ownership and state machine transition.
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
		if err := h.service.sessions.CompareAndSwapStatus(r.Context(), sessionID, "awaiting_confirmation", "confirming"); err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "session state transition failed"})
			return
		}
	}

	// Call Orchestrator to accept the specialist session.
	if err := h.orch.AcceptSession(r.Context(), sessionID, req.Agent); err != nil {
		h.service.setSessionStatus(r.Context(), sessionID, "awaiting_confirmation")
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("orchestrator accept failed: %v", err)})
		return
	}

	// Record confirmation audit event, linked to the router event.
	eventID := h.newID("evt")
	trust := h.service.trustMetadataFromRequest(r)
	_, err := h.service.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		ParentEventID:      h.service.parentEventID(sessionID),
		SessionID:          sessionID,
		EventType:          "session.confirmed",
		Actor:              actor,
		Agent:              req.Agent,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         trust.TrustLevel,
		EnforcementMode:    trust.EnforcementMode,
	})
	if err != nil {
		h.service.setSessionStatus(r.Context(), sessionID, "awaiting_confirmation")
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}
	h.service.rememberEventID(sessionID, eventID)
	if h.service.sessions != nil {
		if err := h.service.sessions.CompareAndSwapStatus(r.Context(), sessionID, "confirming", "confirmed"); err != nil {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "session state transition failed"})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":     sessionID,
		"status":         "confirmed",
		"agent":          req.Agent,
		"audit_event_id": eventID,
	})

	// If event store is wired, trigger execution in the background and stream events.
	if h.events != nil {
		h.service.forgetPrompt(sessionID)
		executor := NewSessionExecutor(h.service, h.orch, h.events)
		executor.newID = h.newID
		executor.RunAsync(sessionID, req.Agent, prompt)
	}
}

func sanitizeRuntimeStreamPayload(payload string) string {
	if strings.Contains(payload, `"patchId"`) && strings.Contains(payload, `"files"`) {
		return "Patch proposal received."
	}
	return payload
}

func timeNow() time.Time {
	return time.Now().UTC()
}
