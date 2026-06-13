package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
