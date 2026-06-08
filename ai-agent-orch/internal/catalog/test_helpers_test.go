package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMinimalCatalog(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "models", "registry.yaml"),
		"models:\n"+
			"  - alias: coding-balanced\n"+
			"    provider: openrouter\n"+
			"    model_id: test-provider/coding-balanced\n"+
			"    fallback_alias: coding-fast\n"+
			"  - alias: coding-fast\n"+
			"    provider: openrouter\n"+
			"    model_id: test-provider/coding-fast\n"+
			"    fallback_alias: null\n"+
			"  - alias: coding-economy\n"+
			"    provider: openrouter\n"+
			"    model_id: test-provider/coding-economy\n"+
			"    fallback_alias: null\n"+
			"  - alias: router-small\n"+
			"    provider: openrouter\n"+
			"    model_id: test-provider/router-small\n"+
			"    fallback_alias: null\n")
	writeAgent(t, root, "core/router-agent", "router-agent", "router-small", "coding-economy", "workspace_write: deny\n", []string{"read_file"}, "# Router\n\nConfig: `./agent.config.yaml`\n")
	writeFile(t, filepath.Join(root, "mcp", "registrations", "repo-classification.yaml"), `server_id: repo-classification
allowed_agents:
  - router-agent
  - bad-agent
  - read-only
  - not-tests
  - duplicate
  - unit-tests
`)
}

func writeAgent(t *testing.T, root, relDir, name, primary, fallback, workspacePermission string, tools []string, markdown string) {
	t.Helper()
	dir := filepath.Join(root, "agents", filepath.FromSlash(relDir))
	writeFile(t, filepath.Join(dir, "agent.md"), markdown)
	writeFile(t, filepath.Join(dir, "agent.config.yaml"), agentConfigYAML(name, primary, fallback, workspacePermission, tools))
	writeFile(t, filepath.Join(dir, "evals", "golden-cases.yaml"), "cases: []\n")
}

func agentConfigYAML(name, primary, fallback, workspacePermission string, tools []string) string {
	var b strings.Builder
	b.WriteString("name: " + name + "\n")
	b.WriteString(`version: 0.1.0
phase: experimental
owner: local
runtime: opencode
model:
  primary: ` + primary + `
  fallback: ` + fallback + `
mcp_servers:
  - repo-classification
tools_allowed:
`)
	for _, tool := range tools {
		b.WriteString("  - " + tool + "\n")
	}
	b.WriteString(`permissions:
  network: deny
  ` + workspacePermission + `  outside_workspace_write: deny
governance:
  classification_max: internal
cost:
  per_invocation_cap_usd: 0.10
  consecutive_tool_call_max: 15
evals:
  path: ./evals/
  required_for_phase0: false
`)
	return b.String()
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
