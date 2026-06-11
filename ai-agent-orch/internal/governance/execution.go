package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"ai-agent-orch/internal/audit"
	patchproto "ai-agent-orch/internal/patch"
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
	startedAt := time.Now().UTC()
	_, _ = e.service.audit.Append(ctx, audit.Event{
		EventID:            e.newID("evt"),
		SessionID:          sessionID,
		EventType:          "runtime.started",
		Actor:              "runtime",
		Agent:              agent,
		Runtime:            "opencode_acp",
		RuntimeStatus:      "running",
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         "gateway_enforced",
		EnforcementMode:    "gateway",
	})

	// Server-side runtimes (the ACP lane) need their own gateway secret: the
	// client's token is never stored in the clear, so a fresh one is minted at
	// dispatch and its hash recorded next to the client hash.
	runtimeToken := e.service.mintRuntimeGatewayToken(ctx, sessionID)

	result, err := e.orch.Dispatch(ctx, sessionID, agent, prompt, runtimeToken)
	if err != nil {
		e.service.setSessionStatus(context.Background(), sessionID, "failed")
		_, _ = e.service.audit.Append(ctx, audit.Event{
			EventID:            e.newID("evt"),
			SessionID:          sessionID,
			EventType:          "runtime.failed",
			Actor:              "runtime",
			Agent:              agent,
			Runtime:            "opencode_acp",
			RuntimeStatus:      "failed",
			DurationMS:         time.Since(startedAt).Milliseconds(),
			Reason:             err.Error(),
			RawPromptStored:    false,
			RawResponseStored:  false,
			CorrelationSubject: "governance-shell",
			TrustLevel:         "gateway_enforced",
			EnforcementMode:    "gateway",
		})
		e.events.Publish(sessionID, SessionEvent{
			Type:      "error",
			Payload:   fmt.Sprintf("dispatch failed: %v", err),
			Timestamp: time.Now().UTC(),
		})
		e.events.Close(sessionID)
		return
	}

	toolLoop := NewToolLoopCounter(e.service.toolLoopMax)
	eventCount := 0
	patchCount := 0
	toolCallCount := 0
	for _, event := range result.Events {
		eventCount++
		if event.Type == "tool" || event.Type == "tool_call" || event.Type == "tool_request" {
			toolCallCount++
		}
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
			patchID := extractPatchID(event.Payload)
			e.service.rememberPatch(sessionID, patchID)
			patchCount++
			e.auditPatchProposed(ctx, sessionID, agent, sanitized)
		}
		if event.Type == "stream" {
			event.Payload = sanitizeRuntimeStreamPayload(event.Payload)
		}
		if event.Type == "acp_permission" {
			e.auditACPPermission(ctx, sessionID, agent, event.Payload)
			continue
		}
		if event.Type == "acp_file_write" {
			e.auditACPFileWrite(ctx, sessionID, agent, event.Payload)
			continue
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
	_, _ = e.service.audit.Append(ctx, audit.Event{
		EventID:            e.newID("evt"),
		SessionID:          sessionID,
		EventType:          "runtime.done",
		Actor:              "runtime",
		Agent:              agent,
		Runtime:            "opencode_acp",
		RuntimeStatus:      "done",
		DurationMS:         time.Since(startedAt).Milliseconds(),
		EventCount:         eventCount,
		PatchCount:         patchCount,
		ToolCallCount:      toolCallCount,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         "gateway_enforced",
		EnforcementMode:    "gateway",
	})
}

func (e *SessionExecutor) auditACPPermission(ctx context.Context, sessionID string, agent string, payload string) {
	var data struct {
		Tool     string `json:"tool"`
		OptionID string `json:"option_id"`
	}
	_ = json.Unmarshal([]byte(payload), &data)
	_, _ = e.service.audit.Append(ctx, audit.Event{
		EventID:            e.newID("evt"),
		SessionID:          sessionID,
		EventType:          "runtime.acp.permission",
		Actor:              "runtime",
		Agent:              agent,
		Runtime:            "opencode_acp",
		MCPToolName:        data.Tool,
		Reason:             data.OptionID,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         "gateway_enforced",
		EnforcementMode:    "gateway",
	})
}

func (e *SessionExecutor) auditACPFileWrite(ctx context.Context, sessionID string, agent string, payload string) {
	var data struct {
		Path    string `json:"path"`
		Action  string `json:"action"`
		PatchID string `json:"patch_id"`
	}
	_ = json.Unmarshal([]byte(payload), &data)
	_, _ = e.service.audit.Append(ctx, audit.Event{
		EventID:            e.newID("evt"),
		SessionID:          sessionID,
		EventType:          "runtime.acp.file_write",
		Actor:              "runtime",
		Agent:              agent,
		Runtime:            "opencode_acp",
		RuntimeStatus:      "file_written",
		PatchID:            data.PatchID,
		PatchFileCount:     1,
		Reason:             data.Action + ": " + data.Path,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         "gateway_enforced",
		EnforcementMode:    "gateway",
	})
}

func (e *SessionExecutor) auditPatchProposed(ctx context.Context, sessionID string, agent string, payload string) {
	if e == nil || e.service == nil || e.service.audit == nil {
		return
	}
	var envelope patchproto.PatchEnvelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return
	}
	_, _ = e.service.audit.Append(ctx, audit.Event{
		EventID:            e.newID("evt"),
		SessionID:          sessionID,
		EventType:          "patch.proposed",
		Actor:              "runtime",
		Agent:              agent,
		PatchID:            envelope.PatchID,
		PatchBufferID:      envelope.BufferID,
		PatchFileCount:     len(envelope.Files),
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         "gateway_enforced",
		EnforcementMode:    "gateway",
	})
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
