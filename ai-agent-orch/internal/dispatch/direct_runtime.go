package dispatch

import (
	"context"
	"fmt"
	"strings"

	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/openrouter"
)

// DirectRuntime calls OpenRouter directly without an ACP subprocess.
// It is the Phase 1 fallback when OpenCode is not installed.
type DirectRuntime struct {
	client      *openrouter.Client
	catalogRoot string
}

func NewDirectRuntime(client *openrouter.Client, catalogRoot string) *DirectRuntime {
	return &DirectRuntime{
		client:      client,
		catalogRoot: catalogRoot,
	}
}

func (r *DirectRuntime) StartSession(ctx context.Context, cfg SessionConfig) (SessionHandle, error) {
	modelID, err := catalog.ResolveOpenRouterModelID(r.catalogRoot, cfg.ModelID)
	if err != nil {
		return nil, fmt.Errorf("resolve model %q: %w", cfg.ModelID, err)
	}

	handle := &directHandle{
		client:  r.client,
		config:  cfg,
		modelID: modelID,
		events:  make(chan RuntimeEvent, 16),
		done:    make(chan struct{}),
	}

	go handle.run(ctx)
	return handle, nil
}

type directHandle struct {
	client  *openrouter.Client
	config  SessionConfig
	modelID string
	events  chan RuntimeEvent
	done    chan struct{}
	err     error
}

func (h *directHandle) run(ctx context.Context) {
	defer close(h.done)
	defer close(h.events)

	systemPrompt := h.config.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful coding assistant. Respond with code changes in a clear format."
	}

	h.events <- RuntimeEvent{
		Type:    "stream",
		Payload: "Starting direct runtime session...",
	}

	req := openrouter.ChatCompletionRequest{
		Model: h.modelID,
		Messages: []openrouter.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: defaultUserPrompt(h.config.UserPrompt)},
		},
		Temperature: 0.2,
		MaxTokens:   4096,
	}

	resp, err := h.client.ChatCompletion(ctx, req)
	if err != nil {
		h.err = err
		h.events <- RuntimeEvent{
			Type:    "error",
			Payload: fmt.Sprintf("OpenRouter call failed: %v", err),
		}
		return
	}

	content := resp.FirstContent()
	h.events <- RuntimeEvent{
		Type:    "stream",
		Payload: content,
	}

	// Try to extract a patch envelope from the response.
	patch := h.tryExtractPatch(content)
	if patch != "" {
		h.events <- RuntimeEvent{
			Type:    "patch",
			Payload: patch,
		}
	}

	h.events <- RuntimeEvent{
		Type:    "done",
		Payload: fmt.Sprintf("session complete (%d tokens)", resp.Usage.TotalTokens),
	}
}

func (h *directHandle) tryExtractPatch(content string) string {
	// Simple heuristic: look for JSON-like patch structure.
	if strings.Contains(content, `"files"`) && strings.Contains(content, `"action"`) {
		start := strings.Index(content, "{")
		end := strings.LastIndex(content, "}")
		if start >= 0 && end > start {
			return content[start : end+1]
		}
	}
	return ""
}

func (h *directHandle) Wait() error {
	<-h.done
	return h.err
}

func (h *directHandle) Events() <-chan RuntimeEvent {
	return h.events
}

func (h *directHandle) Stop() error {
	return nil
}
