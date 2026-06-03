package skillsfactory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallVSCode(t *testing.T) {
	dir := t.TempDir()
	res, err := Install(ClientVSCode, dir, "http://127.0.0.1:18081")
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if len(res.FilesWritten) == 0 {
		t.Fatal("expected files to be written")
	}

	mcpPath := filepath.Join(dir, ".vscode", "mcp.json")
	if _, err := os.Stat(mcpPath); os.IsNotExist(err) {
		t.Fatalf("expected .vscode/mcp.json to exist")
	}

	content, _ := os.ReadFile(mcpPath)
	if !strings.Contains(string(content), "ai-orch-gateway") {
		t.Fatalf("expected mcp.json to contain ai-orch-gateway")
	}
}

func TestInstallCLine(t *testing.T) {
	dir := t.TempDir()
	_, err := Install(ClientCLine, dir, "http://127.0.0.1:18081")
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	rulesPath := filepath.Join(dir, ".clinerules")
	if _, err := os.Stat(rulesPath); os.IsNotExist(err) {
		t.Fatalf("expected .clinerules to exist")
	}

	content, _ := os.ReadFile(rulesPath)
	if !strings.Contains(string(content), "start_governed_session") {
		t.Fatalf("expected .clinerules to mention start_governed_session")
	}
}

func TestInstallClaudeCode(t *testing.T) {
	dir := t.TempDir()
	result, err := Install(ClientClaudeCode, dir, "http://127.0.0.1:18081")
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	claudePath := filepath.Join(dir, "CLAUDE.md")
	if _, err := os.Stat(claudePath); os.IsNotExist(err) {
		t.Fatalf("expected CLAUDE.md to exist")
	}

	mcpPath := filepath.Join(dir, ".mcp.json")
	if _, err := os.Stat(mcpPath); os.IsNotExist(err) {
		t.Fatalf("expected .mcp.json to exist")
	}

	if !strings.Contains(result.Instructions, "Claude Code") {
		t.Fatalf("expected instructions to mention Claude Code")
	}
}

func TestInstallCodex(t *testing.T) {
	dir := t.TempDir()
	_, err := Install(ClientCodex, dir, "http://127.0.0.1:18081")
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}

	agentsPath := filepath.Join(dir, "AGENTS.md")
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		t.Fatalf("expected AGENTS.md to exist")
	}

	content, _ := os.ReadFile(agentsPath)
	if !strings.Contains(string(content), "gateway_enforced") {
		t.Fatalf("expected AGENTS.md to mention gateway_enforced")
	}
}

func TestInstallUnknownClient(t *testing.T) {
	_, err := Install("unknown", t.TempDir(), "http://127.0.0.1:18081")
	if err == nil {
		t.Fatal("expected error for unknown client")
	}
}

func TestInstallRefusesToOverwriteExistingFiles(t *testing.T) {
	dir := t.TempDir()
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("keep me"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Install(ClientCodex, dir, "http://127.0.0.1:18081")
	if err == nil {
		t.Fatal("expected install to refuse overwriting AGENTS.md")
	}

	content, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep me" {
		t.Fatalf("expected existing file to be preserved, got %q", string(content))
	}
}

func TestInstallForceOverwritesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("replace me"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := InstallWithOptions(ClientCodex, dir, "http://127.0.0.1:18081", InstallOptions{Force: true})
	if err != nil {
		t.Fatalf("install with force failed: %v", err)
	}

	content, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "start_governed_session") {
		t.Fatalf("expected forced install to replace file, got: %s", string(content))
	}
}

func TestDoctor(t *testing.T) {
	dir := t.TempDir()
	issues := Doctor(dir, "http://127.0.0.1:18081")
	// Should report missing configs.
	if len(issues) == 0 {
		t.Fatal("expected issues for empty directory")
	}
	found := false
	for _, i := range issues {
		if strings.Contains(i, "AGENTS.md not found") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected AGENTS.md missing issue, got: %v", issues)
	}
}

func TestDoctorAllPresent(t *testing.T) {
	dir := t.TempDir()
	// Create all expected files.
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# AGENTS"), 0644)
	os.MkdirAll(filepath.Join(dir, ".vscode"), 0755)
	os.WriteFile(filepath.Join(dir, ".vscode", "mcp.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, ".clinerules"), []byte("rules"), 0644)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("claude"), 0644)
	os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte("{}"), 0644)

	issues := Doctor(dir, "http://127.0.0.1:18081")
	if len(issues) != 1 || !strings.Contains(issues[0], "All client configurations present") {
		t.Fatalf("expected all present message, got: %v", issues)
	}
}

func TestParseClientType(t *testing.T) {
	tests := []struct {
		input    string
		expected ClientType
		wantErr  bool
	}{
		{"vscode", ClientVSCode, false},
		{"VSCode", ClientVSCode, false},
		{"cline", ClientCLine, false},
		{"claude-code", ClientClaudeCode, false},
		{"claude", ClientClaudeCode, false},
		{"codex", ClientCodex, false},
		{"unknown", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseClientType(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseClientType(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.expected {
				t.Fatalf("ParseClientType(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGenerateAGENTSMarkdown(t *testing.T) {
	md := GenerateAGENTSMarkdown("http://localhost:18081")
	if !strings.Contains(md, "http://localhost:18081") {
		t.Fatal("expected gateway URL in markdown")
	}
	if !strings.Contains(md, "start_governed_session") {
		t.Fatal("expected start_governed_session in markdown")
	}
	if !strings.Contains(md, "gateway_enforced") {
		t.Fatal("expected gateway_enforced in markdown")
	}
}
