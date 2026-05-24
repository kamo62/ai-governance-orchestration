package orchestrator

import (
	"encoding/json"
	"fmt"
	"net/http"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/dispatch"
)

// DispatchRequest is the request to start a specialist runtime.
type DispatchRequest struct {
	Agent  string `json:"agent"`
	Prompt string `json:"prompt"`
}

// DispatchHandler serves POST /v1/orchestrator/dispatch.
type DispatchHandler struct {
	dispatcher *Dispatcher
	audit      AuditStore
	newID      func(prefix string) string
}

func NewDispatchHandler(dispatcher *Dispatcher, audit AuditStore) http.Handler {
	return &DispatchHandler{
		dispatcher: dispatcher,
		audit:      audit,
		newID:      randomID,
	}
}

func (h *DispatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.dispatcher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "dispatcher unavailable"})
		return
	}
	if h.audit == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "audit store unavailable"})
		return
	}

	sessionID := r.Header.Get("X-AI-Orch-Session-ID")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "X-AI-Orch-Session-ID header is required"})
		return
	}

	var req DispatchRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	if req.Agent == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "agent is required"})
		return
	}
	if req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}

	// Start the runtime session asynchronously.
	handle, err := h.dispatcher.Dispatch(r.Context(), req.Agent, req.Prompt)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("dispatch failed: %v", err)})
		return
	}

	// Collect events from the runtime and write them as a simple JSON response
	// for the non-streaming Phase 1 fallback. In Phase 2, this would become SSE.
	var events []dispatch.RuntimeEvent
	for event := range handle.Events() {
		events = append(events, event)
	}

	// Record specialist execution audit event before reporting completion.
	if _, err := h.audit.Append(r.Context(), audit.Event{
		EventID:            h.newID("evt"),
		SessionID:          sessionID,
		EventType:          "specialist.execution",
		Actor:              "local-dev",
		Agent:              req.Agent,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "orchestrator",
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"status":     "completed",
		"agent":      req.Agent,
		"events":     events,
	})
}
