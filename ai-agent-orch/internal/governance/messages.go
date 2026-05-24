package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"ai-agent-orch/internal/audit"
)

// OrchestratorClient is the interface used by the Governance Shell to call the Orchestrator.
type OrchestratorClient interface {
	Route(ctx context.Context, sessionID string, prompt string) (RouteDecision, error)
	AcceptSession(ctx context.Context, sessionID string, agent string) error
	Dispatch(ctx context.Context, sessionID string, agent string, prompt string) (DispatchResult, error)
}

// RouteDecision is the response from the Orchestrator router.
type RouteDecision struct {
	Specialist string `json:"specialist"`
	Reason     string `json:"reason"`
}

// DispatchResult is the response from the Orchestrator dispatch endpoint.
type DispatchResult struct {
	SessionID string          `json:"session_id"`
	Status    string          `json:"status"`
	Agent     string          `json:"agent"`
	Events    []DispatchEvent `json:"events"`
}

// DispatchEvent represents a single runtime event.
type DispatchEvent struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

// MessagesHandler serves POST /v1/sessions/{id}/messages.
type MessagesHandler struct {
	service *SessionService
	orch    OrchestratorClient
	newID   func(prefix string) string
}

func NewMessagesHandler(service *SessionService, orch OrchestratorClient) http.Handler {
	return &MessagesHandler{
		service: service,
		orch:    orch,
		newID:   randomID,
	}
}

func (h *MessagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/messages")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}

	// Re-check secrets on the new prompt.
	if findings := detectSecrets(req.Prompt); len(findings) > 0 {
		if err := h.service.appendDenied(r.Context(), "secret detected in follow-up message", findings, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "secret detected"})
		return
	}

	// Call Orchestrator for routing.
	decision, err := h.orch.Route(r.Context(), sessionID, req.Prompt)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}

	// Record router-selection audit event.
	eventID := h.newID("evt")
	_, err = h.service.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		SessionID:          sessionID,
		EventType:          "router.specialist.selected",
		Actor:              "local-dev",
		Agent:              decision.Specialist,
		Reason:             decision.Reason,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}
	h.service.rememberPrompt(sessionID, req.Prompt)

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":     sessionID,
		"status":         "awaiting_confirmation",
		"specialist":     decision.Specialist,
		"reason":         decision.Reason,
		"audit_event_id": eventID,
	})
}

func extractSessionID(path string, prefix string, suffix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	trimmed := strings.TrimPrefix(path, prefix)
	if !strings.HasSuffix(trimmed, suffix) {
		return ""
	}
	return strings.TrimSuffix(trimmed, suffix)
}
