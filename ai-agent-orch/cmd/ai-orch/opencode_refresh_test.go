package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeOpenCodeConfigRefreshesOnlyAiOrchProvider(t *testing.T) {
	existing := map[string]any{
		"theme": "opencode",
		"provider": map[string]any{
			"moonshot": map[string]any{"name": "Moonshot", "models": map[string]any{"kimi": map[string]any{"name": "Kimi"}}},
			"ai-orch":  map[string]any{"name": "old ai orch", "options": map[string]any{"baseURL": "old"}},
		},
	}
	merged, changed, err := mergeOpenCodeConfigWithOptions(existing, OpenCodeConfigOptions{
		GatewayURL:     "https://models.example.test",
		RuntimeToken:   "air_newtoken",
		ActorSubject:   "dev@example.test",
		Classification: "internal",
		DiscoveredModels: []OpenCodeProviderModel{
			{ID: "copilot-gpt-5-mini", Name: "Governed Copilot GPT-5 Mini"},
			{ID: "copilot-gpt-5.5", Name: "Governed Copilot GPT-5.5"},
		},
		UseDiscoveredModels: true,
	}, true)
	if err != nil {
		t.Fatalf("merge refresh config: %v", err)
	}
	if !changed {
		t.Fatal("expected refresh to change ai-orch provider")
	}
	providers := merged["provider"].(map[string]any)
	if _, ok := providers["moonshot"]; !ok {
		t.Fatalf("expected Moonshot provider to be preserved, got %#v", providers)
	}
	aiOrch := providers["ai-orch"].(map[string]any)
	options := aiOrch["options"].(map[string]any)
	if options["apiKey"] != "air_newtoken" {
		t.Fatalf("expected refreshed runtime token, got %#v", options["apiKey"])
	}
	models := aiOrch["models"].(map[string]any)
	if _, ok := models["copilot-gpt-5-mini"]; !ok {
		t.Fatalf("expected refreshed discovered chat model, got %#v", models)
	}
	// Responses-only models must not leak into the chat provider.
	if _, ok := models["copilot-gpt-5.5"]; ok {
		t.Fatalf("responses-only model leaked into chat provider: %#v", models)
	}
	responses := providers["ai-orch-responses"].(map[string]any)
	respModels := responses["models"].(map[string]any)
	if _, ok := respModels["copilot-gpt-5.5"]; !ok {
		t.Fatalf("expected responses-only model in responses provider, got %#v", respModels)
	}
}

func TestRefreshJobFilesUseAiOrchRoutedWording(t *testing.T) {
	command := []string{"/usr/local/bin/ai-orch", "opencode", "refresh", "--scope", "global"}
	plist := macOSLaunchAgentPlist("com.ai-orch.opencode-refresh", command)
	if !strings.Contains(plist, "AI-Orch-routed OpenCode") {
		t.Fatalf("expected routed wording in launch agent plist, got %s", plist)
	}
	ps := windowsRefreshTaskPowerShell(command)
	if strings.Contains(strings.ToLower(ps), "governed "+"opencode") {
		t.Fatalf("did not expect legacy OpenCode routing wording in task script: %s", ps)
	}
	if !strings.Contains(ps, "AI-Orch-routed OpenCode") {
		t.Fatalf("expected routed wording in windows task script, got %s", ps)
	}
}

func TestWriteOpenCodeRefreshJobCreatesLaunchAgentOnDarwin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := writeOpenCodeRefreshJob("darwin", []string{"/bin/ai-orch", "opencode", "refresh"})
	if err != nil {
		t.Fatalf("write refresh job: %v", err)
	}
	if path != filepath.Join(home, "Library", "LaunchAgents", "com.ai-orch.opencode-refresh.plist") {
		t.Fatalf("unexpected launch agent path: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read launch agent: %v", err)
	}
	if !strings.Contains(string(data), "opencode") {
		t.Fatalf("expected opencode refresh command, got %s", string(data))
	}
}
