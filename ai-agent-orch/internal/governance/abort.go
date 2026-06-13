package governance

import (
	"net/http"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// AbortHandler handles POST /v1/sessions/{id}/abort.
type AbortHandler struct {
	service *SessionService
	events  *EventStore
}

func NewAbortHandler(service *SessionService, events *EventStore) http.Handler {
	return &AbortHandler{service: service, events: events}
}

func (h *AbortHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h == nil || h.service == nil || h.service.audit == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/abort")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	actor := actorFromContext(r.Context())

	// Enforce session ownership before any mutation.
	var currentStatus string
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
		currentStatus = record.Status
	}

	// Record audit event (fail-closed), linked to the prior event chain.
	eventID := h.service.newID("evt")
	_, err := h.service.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		ParentEventID:      h.service.parentEventID(sessionID),
		SessionID:          sessionID,
		EventType:          "session.aborted",
		Actor:              actor,
		CorrelationSubject: "governance-shell",
		RecordedAt:         time.Now().UTC(),
		RawPromptStored:    false,
		RawResponseStored:  false,
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}
	h.service.rememberEventID(sessionID, eventID)
	if h.service.sessions != nil {
		if err := h.service.sessions.CompareAndSwapStatus(r.Context(), sessionID, currentStatus, "aborted"); err != nil {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "session state transition failed"})
			return
		}
	}

	// Cancel any running execution for this session.
	h.service.cancelExecution(sessionID)

	// Publish an abort event so SSE subscribers see it.
	if h.events != nil {
		h.events.Publish(sessionID, SessionEvent{
			Type:      "abort",
			Payload:   "Session aborted by user",
			Timestamp: time.Now().UTC(),
		})
		h.events.Close(sessionID)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"status":     "aborted",
	})
}
