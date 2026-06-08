package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateOpenCodeConfig(t *testing.T) {
	config := GenerateOpenCodeConfig("http://127.0.0.1:18082")

	if config["$schema"] != "https://opencode.ai/config.json" {
		t.Fatalf("unexpected schema: %v", config["$schema"])
	}
	if config["model"] != "ai-orch/coding-balanced" {
		t.Fatalf("unexpected model: %v", config["model"])
	}

	provider, ok := config["provider"].(map[string]any)
	if !ok {
		t.Fatal("expected provider map")
	}

	aiOrch, ok := provider["ai-orch"].(map[string]any)
	if !ok {
		t.Fatal("expected ai-orch provider config")
	}

	options, ok := aiOrch["options"].(map[string]any)
	if !ok {
		t.Fatal("expected options map")
	}

	if options["baseURL"] != "http://127.0.0.1:18082/v1" {
		t.Fatalf("unexpected baseURL: %v", options["baseURL"])
	}
	if options["apiKey"] != "{env:AI_ORCH_RUNTIME_TOKEN}" {
		t.Fatalf("unexpected apiKey: %v", options["apiKey"])
	}
	headers, ok := options["headers"].(map[string]any)
	if !ok {
		t.Fatal("expected headers map")
	}
	if headers["X-AI-Orch-Session-ID"] != "{env:AI_ORCH_SESSION_ID}" {
		t.Fatalf("unexpected session header: %v", headers["X-AI-Orch-Session-ID"])
	}

	models, ok := aiOrch["models"].(map[string]any)
	if !ok {
		t.Fatal("expected models map")
	}
	if _, ok := models["coding-balanced"]; !ok {
		t.Fatal("expected coding-balanced model")
	}
}

func TestOpenCodeBaseURL(t *testing.T) {
	tests := []struct {
		name       string
		gatewayURL string
		want       string
	}{
		{"gateway root", "http://127.0.0.1:18082", "http://127.0.0.1:18082/v1"},
		{"already v1", "http://127.0.0.1:18082/v1", "http://127.0.0.1:18082/v1"},
		{"trailing slash", "http://governance-shell:18082/", "http://governance-shell:18082/v1"},
		{"empty default", "", "http://127.0.0.1:18082/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openCodeBaseURL(tt.gatewayURL); got != tt.want {
				t.Fatalf("openCodeBaseURL(%q) = %q, want %q", tt.gatewayURL, got, tt.want)
			}
		})
	}
}

func TestResolveOpenCodeConfigPathProjectAndGlobal(t *testing.T) {
	project := t.TempDir()
	t.Setenv("OPENCODE_PROJECT_DIR", project)
	if got, err := resolveOpenCodeConfigPath("project", ""); err != nil || got != filepath.Join(project, "opencode.json") {
		t.Fatalf("project config path = %q, %v", got, err)
	}

	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, err := resolveOpenCodeConfigPath("global", ""); err != nil || got != filepath.Join(xdg, "opencode", "opencode.json") {
		t.Fatalf("global config path = %q, %v", got, err)
	}
}

func TestMergeOpenCodeConfigPreservesExistingSettings(t *testing.T) {
	existing := map[string]any{
		"theme":              "opencode",
		"enabled_providers":  []any{"anthropic"},
		"disabled_providers": []any{"ai-orch", "gemini"},
		"provider": map[string]any{
			"anthropic": map[string]any{"models": map[string]any{}},
		},
	}

	merged, changed, err := mergeOpenCodeConfig(existing, "http://127.0.0.1:18083", false)
	if err != nil {
		t.Fatalf("merge config: %v", err)
	}
	if !changed {
		t.Fatal("expected config to change")
	}
	if merged["theme"] != "opencode" {
		t.Fatalf("expected existing theme to be preserved")
	}
	enabled := merged["enabled_providers"].([]string)
	if !contains(enabled, "anthropic") || !contains(enabled, "ai-orch") {
		t.Fatalf("expected enabled providers to include existing and ai-orch, got %#v", enabled)
	}
	disabled := merged["disabled_providers"].([]string)
	if contains(disabled, "ai-orch") || !contains(disabled, "gemini") {
		t.Fatalf("expected ai-orch removed from disabled providers, got %#v", disabled)
	}
	providers := merged["provider"].(map[string]any)
	if _, ok := providers["anthropic"]; !ok {
		t.Fatal("expected existing provider to be preserved")
	}
	aiOrch := providers["ai-orch"].(map[string]any)
	options := aiOrch["options"].(map[string]any)
	if options["baseURL"] != "http://127.0.0.1:18083/v1" {
		t.Fatalf("unexpected ai-orch base URL: %v", options["baseURL"])
	}
	if options["apiKey"] != "{env:AI_ORCH_RUNTIME_TOKEN}" {
		t.Fatalf("unexpected apiKey: %v", options["apiKey"])
	}
}

func TestMergeOpenCodeConfigRequiresForceForDifferentProvider(t *testing.T) {
	existing := map[string]any{
		"provider": map[string]any{
			"ai-orch": map[string]any{"name": "old"},
		},
	}
	if _, _, err := mergeOpenCodeConfig(existing, "http://127.0.0.1:18082", false); err == nil {
		t.Fatal("expected differing ai-orch provider to require force")
	}
	if _, _, err := mergeOpenCodeConfig(existing, "http://127.0.0.1:18082", true); err != nil {
		t.Fatalf("expected force to update provider: %v", err)
	}
}

func TestWriteOpenCodeConfigCreatesBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, []byte("{\"theme\":\"old\"}\n"), 0o600); err != nil {
		t.Fatalf("write existing config: %v", err)
	}
	backup, err := writeOpenCodeConfig(path, map[string]any{"theme": "new"}, 0o600)
	if err != nil {
		t.Fatalf("write config: %v", err)
	}
	if backup == "" {
		t.Fatal("expected backup path")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("expected backup to exist: %v", err)
	}
}

func TestOpenCodeE2EArgs(t *testing.T) {
	dir, model, prompt := openCodeE2EArgs([]string{"--dir", "/tmp/demo", "--model", "ai-orch/coding-fast", "--prompt", "hello"})
	if dir != "/tmp/demo" || model != "ai-orch/coding-fast" || prompt != "hello" {
		t.Fatalf("unexpected args: %q %q %q", dir, model, prompt)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
