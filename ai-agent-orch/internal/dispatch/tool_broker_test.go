package dispatch

import (
	"path/filepath"
	"testing"
)

func TestToolBroker_Validate(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	broker, err := NewToolBroker(path)
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}

	if err := broker.Validate("read_file", "", "any-agent"); err != nil {
		t.Fatalf("read_file should validate: %v", err)
	}
	if err := broker.Validate("run_command", "playwright", "unit-tests"); err != nil {
		t.Fatalf("run_command:playwright for unit-tests should validate: %v", err)
	}
	if err := broker.Validate("run_command", "playwright", "code-review"); err == nil {
		t.Fatal("run_command:playwright for code-review should be denied")
	}
	if err := broker.Validate("run_command", "curl", "unit-tests"); err == nil {
		t.Fatal("run_command:curl should be denied")
	}
}

func TestToolBroker_ValidateWithPermissions(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	broker, err := NewToolBroker(path)
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}

	if err := broker.ValidateWithPermissions("write_file", "", "unit-tests", map[string]string{"workspace_write": "allow"}); err != nil {
		t.Fatalf("write_file should validate with workspace_write allow: %v", err)
	}
	if err := broker.ValidateWithPermissions("write_file", "", "unit-tests", map[string]string{"workspace_write": "deny"}); err == nil {
		t.Fatal("write_file should be denied with workspace_write deny")
	}
}

func TestToolBroker_FailClosed_NotConfigured(t *testing.T) {
	var broker *ToolBroker
	if err := broker.Validate("read_file", "", "any-agent"); err == nil {
		t.Fatal("nil broker should fail closed")
	}
}

func TestParseToolCommand(t *testing.T) {
	cmd, sub := ParseToolCommand("run_command:playwright")
	if cmd != "run_command" || sub != "playwright" {
		t.Fatalf("unexpected parse: %q %q", cmd, sub)
	}

	cmd, sub = ParseToolCommand("read_file")
	if cmd != "read_file" || sub != "" {
		t.Fatalf("unexpected parse: %q %q", cmd, sub)
	}
}
