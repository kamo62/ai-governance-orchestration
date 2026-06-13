package policyengine

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadCommandAllowlistRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "command-allowlists.yaml")
	if err := os.WriteFile(path, []byte(`
system_commands:
  - name: read_file
    description: Read files.
    default: allow
    surprise: nope
`), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	_, err := LoadCommandAllowlist(path)
	if err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
	if !strings.Contains(err.Error(), "field surprise not found") {
		t.Fatalf("unexpected error: %v", err)
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

	if list.IsAllowed("run_command", "curl", "unit-tests") {
		t.Fatal("run_command curl should be denied")
	}
	if list.IsAllowed("run_command", "", "unit-tests") {
		t.Fatal("run_command without subcommand should be denied")
	}
	if list.IsAllowedWithPermissions("write_file", "", "unit-tests", map[string]string{"workspace_write": "deny"}) {
		t.Fatal("write_file should be denied when workspace_write is deny")
	}
}

func TestCommandAllowlist_IsAllowed_Playwright(t *testing.T) {
	path := filepath.Join("..", "..", "policies", "command-allowlists.yaml")
	list, err := LoadCommandAllowlist(path)
	if err != nil {
		t.Fatalf("load allowlist: %v", err)
	}

	if !list.IsAllowed("run_command", "playwright", "unit-tests") {
		t.Fatal("run_command:playwright should be allowed for unit-tests")
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
