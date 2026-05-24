package orchestrator

import "testing"

func TestMCPEndpointEnvKeyNormalizesServerNames(t *testing.T) {
	got := mcpEndpointEnvKey("issue-tracker")
	if got != "MCP_ISSUE_TRACKER_URL" {
		t.Fatalf("unexpected env key %q", got)
	}
}

func TestResolveMCPEndpointsAllowsEnvironmentOverride(t *testing.T) {
	t.Setenv("MCP_ISSUE_TRACKER_URL", "http://override:9000")

	endpoints := resolveMCPEndpoints([]string{"issue-tracker"})
	if endpoints["issue-tracker"] != "http://override:9000" {
		t.Fatalf("expected override endpoint, got %q", endpoints["issue-tracker"])
	}
}
