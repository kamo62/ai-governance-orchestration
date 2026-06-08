package governance

import (
	"context"
	"fmt"
	"time"

	"ai-agent-orch/internal/audit"
)

// SessionExecutor runs governed runtime dispatch and publishes SSE events.
type SessionExecutor struct {
	service *SessionService
	orch    OrchestratorClient
	events  *EventStore
	newID   func(prefix string) string
}

func NewSessionExecutor(service *SessionService, orch OrchestratorClient, events *EventStore) *SessionExecutor {
	return &SessionExecutor{
		service: service,
		orch:    orch,
		events:  events,
		newID:   randomID,
	}
}

func (e *SessionExecutor) PublishStream(sessionID, payload string) {
	if e == nil || e.events == nil {
		return
	}
	e.events.Publish(sessionID, SessionEvent{
		Type:      "stream",
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	})
}

func (e *SessionExecutor) RunAsync(sessionID, agent, prompt string) {
	if e == nil || e.events == nil || e.service == nil {
		return
	}
	e.service.setSessionStatus(context.Background(), sessionID, "running")
	execCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	e.service.registerCancel(sessionID, cancel)
	go func() {
		defer cancel()
		defer e.service.cancelExecution(sessionID)
		e.Run(execCtx, sessionID, agent, prompt)
	}()
}

func (e *SessionExecutor) Run(ctx context.Context, sessionID, agent, prompt string) {
	if e == nil || e.events == nil {
		return
	}

	e.PublishStream(sessionID, fmt.Sprintf("Starting execution for agent %s...", agent))

	result, err := e.orch.Dispatch(ctx, sessionID, agent, prompt)
	if err != nil {
		e.service.setSessionStatus(context.Background(), sessionID, "failed")
		e.events.Publish(sessionID, SessionEvent{
			Type:      "error",
			Payload:   fmt.Sprintf("dispatch failed: %v", err),
			Timestamp: time.Now().UTC(),
		})
		e.events.Close(sessionID)
		return
	}

	toolLoop := NewToolLoopCounter(e.service.toolLoopMax)
	for _, event := range result.Events {
		if toolLoop.Observe(event.Type) {
			reason := fmt.Sprintf("consecutive tool call cap exceeded (%d)", e.service.toolLoopMax)
			_, _ = e.service.audit.Append(ctx, audit.Event{
				EventID:            e.newID("evt"),
				SessionID:          sessionID,
				EventType:          "runtime.denied",
				Actor:              "runtime",
				Agent:              agent,
				Reason:             reason,
				RawPromptStored:    false,
				RawResponseStored:  false,
				CorrelationSubject: "governance-shell",
				TrustLevel:         "gateway_enforced",
				EnforcementMode:    "gateway",
			})
			e.service.setSessionStatus(context.Background(), sessionID, "failed")
			e.events.Publish(sessionID, SessionEvent{
				Type:      "error",
				Payload:   reason,
				Timestamp: time.Now().UTC(),
			})
			e.events.Close(sessionID)
			return
		}
		if event.Type == "patch" {
			sanitized, err := e.service.patchBuffer.Store(ctx, sessionID, event.Payload)
			if err != nil {
				reason := fmt.Sprintf("patch rejected: %v", err)
				_, _ = e.service.audit.Append(ctx, audit.Event{
					EventID:            e.newID("evt"),
					SessionID:          sessionID,
					EventType:          "patch.rejected",
					Actor:              "runtime",
					Agent:              agent,
					Reason:             reason,
					RawPromptStored:    false,
					RawResponseStored:  false,
					CorrelationSubject: "governance-shell",
					TrustLevel:         "gateway_enforced",
					EnforcementMode:    "gateway",
				})
				e.service.setSessionStatus(context.Background(), sessionID, "failed")
				e.events.Publish(sessionID, SessionEvent{
					Type:      "error",
					Payload:   reason,
					Timestamp: time.Now().UTC(),
				})
				e.events.Close(sessionID)
				return
			}
			event.Payload = sanitized
			e.service.rememberPatch(sessionID, extractPatchID(event.Payload))
		}
		if event.Type == "stream" {
			event.Payload = sanitizeRuntimeStreamPayload(event.Payload)
		}
		if event.Type == "tool" || event.Type == "tool_call" || event.Type == "tool_request" {
			e.publishToolRequest(sessionID, event.Payload)
			continue
		}
		e.events.Publish(sessionID, SessionEvent{
			Type:      event.Type,
			Payload:   event.Payload,
			Timestamp: time.Now().UTC(),
		})
	}

	e.events.Publish(sessionID, SessionEvent{
		Type:      "done",
		Payload:   fmt.Sprintf("execution finished for %s", result.Agent),
		Timestamp: time.Now().UTC(),
	})
	e.events.Close(sessionID)
	e.service.setSessionStatus(context.Background(), sessionID, "done")
}

func (e *SessionExecutor) publishToolRequest(sessionID, payload string) {
	if e == nil || e.events == nil {
		return
	}
	e.events.Publish(sessionID, SessionEvent{
		Type:      "tool_request",
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	})
}
