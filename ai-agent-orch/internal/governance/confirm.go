package governance

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// ConfirmHandler serves POST /v1/sessions/{id}/confirm.
type ConfirmHandler struct {
	service *SessionService
	orch    OrchestratorClient
	events  *EventStore
	newID   func(prefix string) string
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

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/confirm")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	var req struct {
		Agent          string `json:"agent"`
		HumanConfirmed bool   `json:"human_confirmed,omitempty"`
	}
	if err := readJSON(w, r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.Agent == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "agent is required"})
		return
	}
	if blocked, reason := h.service.blockedByKillSwitch(req.Agent); blocked {
		if err := h.service.appendDenied(r.Context(), reason, nil, ""); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		httpx.WriteJSON(w, http.StatusLocked, map[string]any{"error": reason})
		return
	}

	var prompt string
	if h.events != nil {
		var ok bool
		prompt, ok = h.service.promptForSession(sessionID)
		if !ok {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "session prompt unavailable"})
			return
		}
	}

	// Enforce session ownership and state machine transition.
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
		if record.RoutedAgent != "" && record.RoutedAgent != req.Agent {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "confirmed agent does not match routed specialist"})
			return
		}
		if record.HumanConfirmationRequired && !req.HumanConfirmed {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{
				"error":                       "human confirmation required",
				"status":                      "awaiting_confirmation",
				"next_gate":                   "confirm",
				"human_confirmation_required": true,
			})
			return
		}
		if err := h.service.sessions.CompareAndSwapStatus(r.Context(), sessionID, "awaiting_confirmation", "confirming"); err != nil {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "session state transition failed"})
			return
		}
	}

	// Call Orchestrator to accept the specialist session.
	if err := h.orch.AcceptSession(r.Context(), sessionID, req.Agent); err != nil {
		h.service.setSessionStatus(r.Context(), sessionID, "awaiting_confirmation")
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("orchestrator accept failed: %v", err)})
		return
	}

	// Record confirmation audit event, linked to the router event.
	eventID := h.newID("evt")
	trust := h.service.trustMetadataFromRequest(r)
	findings := []string{}
	if req.HumanConfirmed {
		findings = append(findings, "human_confirmed=true")
	}
	_, err := h.service.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		ParentEventID:      h.service.parentEventID(sessionID),
		SessionID:          sessionID,
		EventType:          "session.confirmed",
		Actor:              actor,
		Agent:              req.Agent,
		Findings:           findings,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         trust.TrustLevel,
		EnforcementMode:    trust.EnforcementMode,
	})
	if err != nil {
		h.service.setSessionStatus(r.Context(), sessionID, "awaiting_confirmation")
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}
	h.service.rememberEventID(sessionID, eventID)
	if h.service.sessions != nil {
		if err := h.service.sessions.CompareAndSwapStatus(r.Context(), sessionID, "confirming", "confirmed"); err != nil {
			httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": "session state transition failed"})
			return
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
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
