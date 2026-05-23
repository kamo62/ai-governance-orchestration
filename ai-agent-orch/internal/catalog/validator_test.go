package catalog

import (
	"path/filepath"
	"testing"
)

func TestValidateCatalogAcceptsCurrentPhase0Catalog(t *testing.T) {
	root := filepath.Join("..", "..")

	report, err := Validate(root)
	if err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}

	if len(report.Agents) != 7 {
		t.Fatalf("expected 7 agents, got %d: %#v", len(report.Agents), report.Agents)
	}
	if len(report.ModelAliases) != 6 {
		t.Fatalf("expected 6 model aliases, got %d: %#v", len(report.ModelAliases), report.ModelAliases)
	}
	if !report.HasAgent("test-generation") {
		t.Fatalf("expected test-generation agent in report")
	}
}

func TestValidateCatalogRejectsExecutablePolicyInAgentMarkdown(t *testing.T) {
	root := t.TempDir()
	writeMinimalCatalog(t, root)
	writeAgent(t, root, "temp/bad-agent", "bad-agent", "coding-balanced", "coding-fast", "workspace_write: allow\n", []string{"read_file"}, "# Bad Agent\n\nConfig: `./agent.config.yaml`\n\nruntime: opencode\n")

	_, err := Validate(root)
	if err == nil {
		t.Fatalf("expected executable policy in agent.md to fail validation")
	}
}

func TestValidateCatalogRejectsUnknownModelAlias(t *testing.T) {
	root := t.TempDir()
	writeMinimalCatalog(t, root)
	writeAgent(t, root, "temp/bad-agent", "bad-agent", "missing-alias", "coding-fast", "workspace_write: allow\n", []string{"read_file"}, "# Bad Agent\n\nConfig: `./agent.config.yaml`\n")

	_, err := Validate(root)
	if err == nil {
		t.Fatalf("expected unknown model alias to fail validation")
	}
}

func TestValidateCatalogRejectsWriteToolForReadOnlyAgent(t *testing.T) {
	root := t.TempDir()
	writeMinimalCatalog(t, root)
	writeAgent(t, root, "temp/read-only", "read-only", "coding-balanced", "coding-fast", "workspace_write: deny\n", []string{"read_file", "write_file"}, "# Read Only\n\nConfig: `./agent.config.yaml`\n")

	_, err := Validate(root)
	if err == nil {
		t.Fatalf("expected write_file with workspace_write deny to fail validation")
	}
}

func TestValidateCatalogRejectsPlaywrightCommandOutsideTestGeneration(t *testing.T) {
	root := t.TempDir()
	writeMinimalCatalog(t, root)
	writeAgent(t, root, "temp/not-tests", "not-tests", "coding-balanced", "coding-fast", "workspace_write: allow\n", []string{"read_file", "run_command:playwright"}, "# Not Tests\n\nConfig: `./agent.config.yaml`\n")

	_, err := Validate(root)
	if err == nil {
		t.Fatalf("expected run_command:playwright outside test-generation to fail validation")
	}
}

func TestValidateCatalogRequiresRouterCasesForTempAgents(t *testing.T) {
	root := t.TempDir()
	writeMinimalCatalog(t, root)
	writeAgent(t, root, "core/router-agent", "router-agent", "router-small", "coding-economy", "workspace_write: deny\n", []string{"read_file"}, "# Router\n\nConfig: `./agent.config.yaml`\n")
	writeAgent(t, root, "temp/test-generation", "test-generation", "coding-balanced", "coding-fast", "workspace_write: allow\n", []string{"read_file"}, "# Test Generation\n\nConfig: `./agent.config.yaml`\n")

	_, err := Validate(root)
	if err == nil {
		t.Fatalf("expected missing router golden case for temp agent to fail validation")
	}
}
