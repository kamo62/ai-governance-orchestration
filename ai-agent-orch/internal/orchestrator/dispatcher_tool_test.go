package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-agent-orch/internal/dispatch"
)

func mustToolBroker(t *testing.T) *dispatch.ToolBroker {
	t.Helper()
	broker, err := dispatch.NewToolBroker(filepath.Join("..", "..", "policies", "command-allowlists.yaml"))
	if err != nil {
		t.Fatalf("new tool broker: %v", err)
	}
	return broker
}

func TestDispatcher_ToolBrokerFailClosedWhenUnavailable(t *testing.T) {
	d := &Dispatcher{}
	err := d.validateAllowedTools("test-generation", []string{"read_file"}, nil)
	if err == nil {
		t.Fatal("expected missing broker to fail closed")
	}
	if !strings.Contains(err.Error(), "tool broker unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDispatcher_ToolBrokerAllowsWriteWhenWorkspaceWriteAllowed(t *testing.T) {
	d := &Dispatcher{broker: mustToolBroker(t)}
	err := d.validateAllowedTools("test-generation", []string{"write_file"}, map[string]string{"workspace_write": "allow"})
	if err != nil {
		t.Fatalf("expected write_file to validate with workspace_write allow: %v", err)
	}
}

func TestDispatcher_ToolBrokerBlocksBadTool(t *testing.T) {
	// Use the real catalog root so the broker can load the allowlist.
	root := realCatalogRoot()
	d := NewDispatcher(root)

	// test-generation is allowed run_command:playwright but not curl.
	// We simulate a tampered catalog that includes a disallowed tool.
	// Since the real catalog is valid, we test the broker path by
	// calling the broker directly.
	if d.broker == nil {
		t.Fatal("tool broker should load from the real policy file")
	}

	// Direct broker check: curl should be denied for test-generation.
	if err := d.broker.Validate("run_command", "curl", "test-generation"); err == nil {
		t.Fatal("expected broker to deny run_command:curl")
	}

	// Direct broker check: read_file should be allowed.
	if err := d.broker.Validate("read_file", "", "any-agent"); err != nil {
		t.Fatalf("expected broker to allow read_file: %v", err)
	}
}

func TestDispatcher_ToolBrokerLoadedFromPolicies(t *testing.T) {
	root := realCatalogRoot()
	// Verify the policy file exists.
	path := filepath.Join(root, "policies", "command-allowlists.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("policies path not available")
	}
	d := NewDispatcher(root)
	if d.broker == nil {
		t.Fatal("tool broker should load from the real policy file")
	}
}
