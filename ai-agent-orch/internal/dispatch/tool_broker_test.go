package dispatch

import (
	"path/filepath"
	"testing"
)

func TestLoadCommandAllowlist(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	list, err := LoadCommandAllowlist(path)
	if err != nil {
		t.Fatalf("load allowlist: %v", err)
	}
	if len(list.SystemCommands) == 0 {
		t.Fatal("expected system commands")
	}
}

func TestCommandAllowlist_IsAllowed_ReadFile(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	list, err := LoadCommandAllowlist(path)
	if err != nil {
		t.Fatalf("load allowlist: %v", err)
	}

	if !list.IsAllowed("read_file", "", "any-agent") {
		t.Fatal("read_file should be allowed by default")
	}
}

func TestCommandAllowlist_IsAllowed_DeniedByDefault(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	list, err := LoadCommandAllowlist(path)
	if err != nil {
		t.Fatalf("load allowlist: %v", err)
	}

	if list.IsAllowed("run_command", "curl", "test-generation") {
		t.Fatal("run_command curl should be denied")
	}
	if list.IsAllowed("run_command", "", "test-generation") {
		t.Fatal("run_command without subcommand should be denied")
	}
	if list.IsAllowedWithPermissions("write_file", "", "test-generation", map[string]string{"workspace_write": "deny"}) {
		t.Fatal("write_file should be denied when workspace_write is deny")
	}
}

func TestCommandAllowlist_IsAllowed_Playwright(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	list, err := LoadCommandAllowlist(path)
	if err != nil {
		t.Fatalf("load allowlist: %v", err)
	}

	if !list.IsAllowed("run_command", "playwright", "test-generation") {
		t.Fatal("run_command:playwright should be allowed for test-generation")
	}
	if list.IsAllowed("run_command", "playwright", "code-review") {
		t.Fatal("run_command:playwright should be denied for code-review")
	}
}

func TestCommandAllowlist_IsAllowed_UnknownCommand(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	list, err := LoadCommandAllowlist(path)
	if err != nil {
		t.Fatalf("load allowlist: %v", err)
	}

	if list.IsAllowed("curl", "", "any-agent") {
		t.Fatal("unknown command should be denied")
	}
}

func TestCommandAllowlist_FailClosed_Nil(t *testing.T) {
	var list *CommandAllowlist
	if list.IsAllowed("read_file", "", "any-agent") {
		t.Fatal("nil allowlist should be fail-closed")
	}
}

func TestToolBroker_Validate(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	broker, err := NewToolBroker(path)
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}

	if err := broker.Validate("read_file", "", "any-agent"); err != nil {
		t.Fatalf("read_file should validate: %v", err)
	}
	if err := broker.Validate("run_command", "playwright", "test-generation"); err != nil {
		t.Fatalf("run_command:playwright for test-generation should validate: %v", err)
	}
	if err := broker.Validate("run_command", "playwright", "code-review"); err == nil {
		t.Fatal("run_command:playwright for code-review should be denied")
	}
	if err := broker.Validate("run_command", "curl", "test-generation"); err == nil {
		t.Fatal("run_command:curl should be denied")
	}
}

func TestToolBroker_ValidateWithPermissions(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	broker, err := NewToolBroker(path)
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}

	if err := broker.ValidateWithPermissions("write_file", "", "test-generation", map[string]string{"workspace_write": "allow"}); err != nil {
		t.Fatalf("write_file should validate with workspace_write allow: %v", err)
	}
	if err := broker.ValidateWithPermissions("write_file", "", "test-generation", map[string]string{"workspace_write": "deny"}); err == nil {
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
