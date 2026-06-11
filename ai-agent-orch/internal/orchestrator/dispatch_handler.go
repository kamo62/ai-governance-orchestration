package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/dispatch"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
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
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.dispatcher == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "dispatcher unavailable"})
		return
	}
	if h.audit == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "audit store unavailable"})
		return
	}

	sessionID := r.Header.Get("X-AI-Orch-Session-ID")
	if sessionID == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "X-AI-Orch-Session-ID header is required"})
		return
	}

	var req DispatchRequest
	if err := readJSON(w, r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.Agent == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "agent is required"})
		return
	}
	if req.Prompt == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}

	// Start the runtime session with a bounded context that is not cancelled
	// merely because the internal HTTP caller disconnects while the runtime is
	// still producing governed evidence.
	runtimeCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	gatewayToken := r.Header.Get("X-AI-Orch-Session-Token")
	handle, err := h.dispatcher.Dispatch(runtimeCtx, sessionID, req.Agent, req.Prompt, gatewayToken)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("dispatch failed: %v", err)})
		return
	}

	// Collect events from the runtime and write them as a simple JSON response
	// for the non-streaming Phase 1 fallback. In Phase 2, this would become SSE.
	var events []dispatch.RuntimeEvent
	var runtimeError string
	patchCount := 0
	toolCallCount := 0
	startedAt := time.Now()
	for event := range handle.Events() {
		events = append(events, event)
		if event.Type == "patch" {
			patchCount++
		}
		if event.Type == "tool" || event.Type == "tool_call" || event.Type == "tool_request" {
			toolCallCount++
		}
		if event.Type == "error" && runtimeError == "" {
			runtimeError = event.Payload
		}
	}
	if err := handle.Wait(); err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error":      fmt.Sprintf("runtime failed: %v", err),
			"session_id": sessionID,
			"agent":      req.Agent,
			"events":     events,
		})
		return
	}
	if runtimeError != "" {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{
			"error":      runtimeError,
			"session_id": sessionID,
			"agent":      req.Agent,
			"events":     events,
		})
		return
	}

	// Record specialist execution audit event before reporting completion.
	if _, err := h.audit.Append(runtimeCtx, audit.Event{
		EventID:            h.newID("evt"),
		SessionID:          sessionID,
		EventType:          "specialist.execution",
		Actor:              "local-dev",
		Agent:              req.Agent,
		Runtime:            "opencode_acp",
		RuntimeStatus:      "completed",
		DurationMS:         time.Since(startedAt).Milliseconds(),
		EventCount:         len(events),
		PatchCount:         patchCount,
		ToolCallCount:      toolCallCount,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "orchestrator",
	}); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"status":     "completed",
		"agent":      req.Agent,
		"events":     events,
	})
}
