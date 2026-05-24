package dispatch

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Runtime is the interface for starting specialist runtime sessions.
type Runtime interface {
	StartSession(ctx context.Context, cfg SessionConfig) (SessionHandle, error)
}

// SessionConfig holds the rendered configuration for a runtime session.
type SessionConfig struct {
	SystemPrompt  string
	UserPrompt    string
	ModelID       string
	ModelProvider string
	AllowedTools  []string
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
	if prompt != "" {
		return prompt
	}
	return "Please help with the requested task. Return any code changes in a structured format."
}
