package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/dispatch"
)

func TestDispatcherBetaSmokeUsesEchoRuntime(t *testing.T) {
	t.Setenv("AI_ORCH_BETA_SMOKE", "true")
	t.Cleanup(func() { os.Unsetenv("AI_ORCH_BETA_SMOKE") })

	root := filepath.Join("..", "..")
	d := NewDispatcher(root)
	handle, err := d.Dispatch(context.Background(), "sess_beta_1", "unit-tests", "write tests for login", "")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	var sawPatch bool
	for event := range handle.Events() {
		if event.Type == "patch" {
			sawPatch = true
		}
	}
	if err := handle.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !sawPatch {
		t.Fatal("expected echo patch event in beta smoke dispatch")
	}
}

func TestDispatcherFailsClosedWithoutRealRuntime(t *testing.T) {
	t.Setenv("AI_ORCH_BETA_SMOKE", "false")
	t.Cleanup(func() { os.Unsetenv("AI_ORCH_BETA_SMOKE") })

	d := &Dispatcher{
		catalogRoot: filepath.Join("..", ".."),
		broker:      mustToolBroker(t),
		runtimes:    map[string]dispatch.Runtime{},
	}
	handle, err := d.Dispatch(context.Background(), "sess_no_runtime", "unit-tests", "write tests for login", "")
	if err == nil {
		t.Fatal("expected dispatch to fail closed when no real runtime is available")
	}
	if handle != nil {
		t.Fatalf("expected nil handle on fail-closed dispatch, got %#v", handle)
	}
	if !strings.Contains(err.Error(), "no real runtime available") {
		t.Fatalf("expected no-real-runtime error, got %v", err)
	}
}
