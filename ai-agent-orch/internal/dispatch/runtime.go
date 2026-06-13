package dispatch

import (
	"context"
	"strings"
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
	// RuntimeName is the concrete engine that produced this session, used for
	// truthful audit attribution instead of handler-side guesses.
	RuntimeName() string
	Wait() error
	Events() <-chan RuntimeEvent
	Stop() error
}

// RuntimeEvent represents an event emitted by the runtime.
type RuntimeEvent struct {
	Type    string `json:"type"` // stream, patch, error, done
	Payload string `json:"payload"`
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
