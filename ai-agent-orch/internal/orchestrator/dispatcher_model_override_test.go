package orchestrator

import (
	"context"
	"path/filepath"
	"testing"

	"ai-agent-orch/internal/dispatch"
)

func TestDispatcherUsesModelAliasOverride(t *testing.T) {
	t.Setenv("AI_ORCH_MODEL_ALIAS_OVERRIDE", "coding-gpt55")
	runtime := &capturingRuntime{}
	dispatcher := &Dispatcher{
		catalogRoot: filepath.Join("..", ".."),
		broker:      mustToolBroker(t),
		runtimes: map[string]dispatch.Runtime{
			"direct": runtime,
		},
	}

	_, err := dispatcher.Dispatch(context.Background(), "sess_test", "unit-tests", "write tests", "")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if runtime.config.ModelID != "coding-gpt55" {
		t.Fatalf("expected override model alias, got %q", runtime.config.ModelID)
	}
}

type capturingRuntime struct {
	config dispatch.SessionConfig
}

func (r *capturingRuntime) StartSession(_ context.Context, cfg dispatch.SessionConfig) (dispatch.SessionHandle, error) {
	r.config = cfg
	return &emptyHandle{}, nil
}

type emptyHandle struct{}

func (h *emptyHandle) Wait() error { return nil }

func (h *emptyHandle) Events() <-chan dispatch.RuntimeEvent {
	ch := make(chan dispatch.RuntimeEvent)
	close(ch)
	return ch
}

func (h *emptyHandle) Stop() error { return nil }
