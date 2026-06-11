package dispatch

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runtime is the interface for starting specialist runtime sessions.
type Runtime interface {
	StartSession(ctx context.Context, cfg SessionConfig) (SessionHandle, error)
}

// SessionConfig holds the rendered configuration for a runtime session.
type SessionConfig struct {
	SessionID string
	// GatewayToken is the per-session model gateway secret minted at dispatch
	// time, exposed to the runtime as AI_ORCH_SESSION_TOKEN.
	GatewayToken  string
	SystemPrompt  string
	UserPrompt    string
	ModelID       string
	ModelProvider string
	WorkspacePath string
	AllowedTools  []string
	AgentName     string
	ToolBroker    *ToolBroker
	MCPEndpoints  map[string]string
	Permissions   map[string]string
	CostCapUSD    float64
}

// SessionHandle abstracts a running runtime session.
type SessionHandle interface {
	Wait() error
	Events() <-chan RuntimeEvent
	Stop() error
}

// RuntimeEvent represents an event emitted by the runtime.
type RuntimeEvent struct {
	Type    string `json:"type"` // stream, patch, error, done
	Payload string `json:"payload"`
}

// OpenCodeRuntime implements Runtime using opencode acp subprocess.
type OpenCodeRuntime struct {
	binaryPath string
	timeout    time.Duration
}

func NewOpenCodeRuntime(binaryPath string) *OpenCodeRuntime {
	if binaryPath == "" {
		binaryPath = "opencode"
	}
	return &OpenCodeRuntime{
		binaryPath: binaryPath,
		timeout:    15 * time.Minute,
	}
}

func (r *OpenCodeRuntime) StartSession(ctx context.Context, cfg SessionConfig) (SessionHandle, error) {
	// Phase 1 stub: validate config but do not start a real ACP session yet.
	// This proves the dispatch path exists and can be exercised in tests.
	if cfg.ModelID == "" {
		return nil, fmt.Errorf("model_id is required")
	}

	// Check binary exists.
	if _, err := exec.LookPath(r.binaryPath); err != nil {
		return &stubHandle{
			events: []RuntimeEvent{
				{Type: "error", Payload: fmt.Sprintf("opencode not found: %v", err)},
			},
		}, nil
	}

	return &stubHandle{
		events: []RuntimeEvent{
			{Type: "stream", Payload: "OpenCode ACP session stub started."},
			{Type: "done", Payload: "session complete"},
		},
	}, nil
}

type stubHandle struct {
	events []RuntimeEvent
	ch     chan RuntimeEvent
}

func (h *stubHandle) Wait() error {
	return nil
}

func (h *stubHandle) Events() <-chan RuntimeEvent {
	if h.ch == nil {
		h.ch = make(chan RuntimeEvent, len(h.events))
		for _, e := range h.events {
			h.ch <- e
		}
		close(h.ch)
	}
	return h.ch
}

func (h *stubHandle) Stop() error {
	return nil
}

func defaultUserPrompt(prompt string) string {
	base := strings.TrimSpace(prompt)
	if base == "" {
		base = "Please help with the requested task."
	}
	return base + `

Runtime patch protocol:
- When the task creates, modifies, or deletes files, return only one JSON object.
- The JSON object must include protocolVersion, patchId, summary, and files.
- Each file entry must include path, action, and newContent for create or modify actions.
- Do not include passwords, tokens, API keys, credentials, private URLs, or other secrets.
- Do not wrap the JSON object in Markdown fences.`
}
