package skillsfactory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"
)

const kiroTestGateway = "http://127.0.0.1:18081"

// kiroTargetRels are the workspace-relative paths installKiro must write.
func kiroTargetRels() []string {
	return []string{
		filepath.Join(".kiro", "settings", "mcp.json"),
		filepath.Join(".kiro", "steering", "ai-orch.md"),
		filepath.Join(".kiro", "hooks", "ai-orch-prompt-submit.kiro.hook.json"),
		filepath.Join(".kiro", "hooks", "ai-orch-post-tool.kiro.hook.json"),
		filepath.Join(".kiro", "hooks", "ai-orch-stop.kiro.hook.json"),
	}
}

// knownClientTokens are the strings ParseClientType resolves without error.
var knownClientTokens = map[string]bool{
	"vscode": true, "cline": true, "claude-code": true,
	"claude": true, "codex": true, "kiro": true,
}

// Feature: governed-client-integration, Property 1: every case-permutation of
// "kiro" parses to ClientKiro with no error.
func TestProperty1_KiroCaseInsensitiveParse(t *testing.T) {
	const word = "kiro"
	// mask flips the case of each of the 4 letters; testing/quick drives the
	// space and we map any mask to a valid case permutation of "kiro".
	f := func(mask uint8) bool {
		b := []byte(word)
		for i := range b {
			if mask&(1<<uint(i)) != 0 {
				b[i] = byte(strings.ToUpper(string(b[i]))[0])
			}
		}
		got, err := ParseClientType(string(b))
		return err == nil && got == ClientKiro
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 256}); err != nil {
		t.Fatal(err)
	}
}

// Feature: governed-client-integration, Property 2: unknown strings error and
// the message contains the input.
func TestProperty2_UnknownClientNamedInError(t *testing.T) {
	f := func(s string) bool {
		if knownClientTokens[strings.ToLower(s)] {
			return true // not an unknown token; skip
		}
		_, err := ParseClientType(s)
		return err != nil && strings.Contains(err.Error(), s)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatal(err)
	}
}

// gatherFiles returns the set of regular file paths under root.
func gatherFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	files := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files[path] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// Feature: governed-client-integration, Property 3: FilesWritten equals the
// files actually created on disk.
func TestProperty3_FilesWrittenMatchesDisk(t *testing.T) {
	f := func(seed uint16) bool {
		dir := t.TempDir()
		res, err := InstallWithOptions(ClientKiro, dir, kiroTestGateway, InstallOptions{})
		if err != nil {
			return false
		}
		onDisk := gatherFiles(t, dir)
		if len(onDisk) != len(res.FilesWritten) {
			return false
		}
		for _, p := range res.FilesWritten {
			if !onDisk[p] {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// Feature: governed-client-integration, Property 4: a pre-existing target
// without force errors naming the file + --force, modifying nothing.
func TestProperty4_ExistingTargetWithoutForceRefused(t *testing.T) {
	rels := kiroTargetRels()
	f := func(idx uint8) bool {
		rel := rels[int(idx)%len(rels)]
		dir := t.TempDir()
		victim := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(victim), 0755); err != nil {
			return false
		}
		const sentinel = "do-not-touch"
		if err := os.WriteFile(victim, []byte(sentinel), 0644); err != nil {
			return false
		}

		_, err := InstallWithOptions(ClientKiro, dir, kiroTestGateway, InstallOptions{})
		if err == nil {
			return false
		}
		base := filepath.Base(rel)
		if !strings.Contains(err.Error(), base) || !strings.Contains(err.Error(), "--force") {
			return false
		}
		// The conflicting file is untouched.
		content, readErr := os.ReadFile(victim)
		if readErr != nil || string(content) != sentinel {
			return false
		}
		// No other target file was created (fail-closed before any write).
		for _, other := range rels {
			if other == rel {
				continue
			}
			if _, statErr := os.Stat(filepath.Join(dir, other)); statErr == nil {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// Feature: governed-client-integration, Property 5: with force, every target is
// overwritten with freshly generated content.
func TestProperty5_ForceOverwritesAllTargets(t *testing.T) {
	rels := kiroTargetRels()
	f := func(mask uint8) bool {
		dir := t.TempDir()
		// Seed an arbitrary subset of targets with stale content.
		for i, rel := range rels {
			if mask&(1<<uint(i)) == 0 {
				continue
			}
			p := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				return false
			}
			if err := os.WriteFile(p, []byte("STALE"), 0644); err != nil {
				return false
			}
		}

		res, err := InstallWithOptions(ClientKiro, dir, kiroTestGateway, InstallOptions{Force: true})
		if err != nil {
			return false
		}
		if len(res.FilesWritten) != len(rels) {
			return false
		}
		// Every target exists with fresh (non-stale) content.
		for _, rel := range rels {
			content, readErr := os.ReadFile(filepath.Join(dir, rel))
			if readErr != nil || string(content) == "STALE" || len(content) == 0 {
				return false
			}
		}
		return true
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// kiroIssues filters Doctor output to Kiro-specific issues.
func kiroIssues(issues []string) []string {
	var out []string
	for _, i := range issues {
		if strings.Contains(i, ".kiro") {
			out = append(out, i)
		}
	}
	return out
}

// Feature: governed-client-integration, Property 6: install-then-Doctor on an
// empty workspace yields zero Kiro issues.
func TestProperty6_InstallThenDoctorClean(t *testing.T) {
	f := func(seed uint16) bool {
		dir := t.TempDir()
		if _, err := InstallWithOptions(ClientKiro, dir, kiroTestGateway, InstallOptions{}); err != nil {
			return false
		}
		return len(kiroIssues(Doctor(dir, kiroTestGateway))) == 0
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatal(err)
	}
}

// --- Example tests (Task 1.5): generated content ---

func TestKiroMCPConfigShape(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallWithOptions(ClientKiro, dir, kiroTestGateway, InstallOptions{}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ".kiro", "settings", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "}\n") {
		t.Fatalf("expected trailing newline on mcp.json, got %q", string(raw))
	}

	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("mcp.json is not valid JSON: %v", err)
	}
	srv, ok := cfg.MCPServers["ai-orch-gateway"]
	if !ok {
		t.Fatalf("expected ai-orch-gateway server, got %+v", cfg.MCPServers)
	}
	if srv.Command != "ai-orch" {
		t.Fatalf("expected command ai-orch, got %q", srv.Command)
	}
	wantArgs := []string{"mcp", "start", "--transport", "stdio"}
	if strings.Join(srv.Args, " ") != strings.Join(wantArgs, " ") {
		t.Fatalf("expected stdio args %v, got %v", wantArgs, srv.Args)
	}
	if srv.Env["AI_ORCH_GOVERNANCE_URL"] != kiroTestGateway {
		t.Fatalf("expected gateway URL in env, got %q", srv.Env["AI_ORCH_GOVERNANCE_URL"])
	}
}

func TestKiroSteeringFilePresent(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallWithOptions(ClientKiro, dir, kiroTestGateway, InstallOptions{}); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	steeringDir := filepath.Join(dir, ".kiro", "steering")
	entries, err := os.ReadDir(steeringDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one steering file")
	}
	content, err := os.ReadFile(filepath.Join(steeringDir, "ai-orch.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), kiroTestGateway) {
		t.Fatal("expected steering file to embed the gateway URL")
	}
}

func TestKiroHookFilesInvokeCorrectSubcommands(t *testing.T) {
	dir := t.TempDir()
	if _, err := InstallWithOptions(ClientKiro, dir, kiroTestGateway, InstallOptions{}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	cases := []struct {
		file     string
		whenType string
		command  string
	}{
		{"ai-orch-prompt-submit.kiro.hook.json", "promptSubmit", "ai-orch hook prompt-submit"},
		{"ai-orch-post-tool.kiro.hook.json", "postToolUse", "ai-orch hook post-tool"},
		{"ai-orch-stop.kiro.hook.json", "agentStop", "ai-orch hook stop"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, ".kiro", "hooks", c.file))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(string(raw), "}\n") {
				t.Fatalf("expected trailing newline, got %q", string(raw))
			}
			var hook struct {
				Name    string `json:"name"`
				Version string `json:"version"`
				When    struct {
					Type      string   `json:"type"`
					ToolTypes []string `json:"toolTypes"`
				} `json:"when"`
				Then struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"then"`
			}
			if err := json.Unmarshal(raw, &hook); err != nil {
				t.Fatalf("hook config not valid JSON: %v", err)
			}
			if hook.When.Type != c.whenType {
				t.Fatalf("expected when.type %q, got %q", c.whenType, hook.When.Type)
			}
			if hook.Then.Type != "runCommand" {
				t.Fatalf("expected then.type runCommand, got %q", hook.Then.Type)
			}
			if hook.Then.Command != c.command {
				t.Fatalf("expected command %q, got %q", c.command, hook.Then.Command)
			}
			if c.whenType == "postToolUse" {
				if strings.Join(hook.When.ToolTypes, ",") != "write,edit" {
					t.Fatalf("expected toolTypes [write edit], got %v", hook.When.ToolTypes)
				}
			}
		})
	}
}

// --- Example tests (Task 1.7): doctor messages ---

func TestDoctorNamesMissingKiroFiles(t *testing.T) {
	dir := t.TempDir()
	issues := Doctor(dir, kiroTestGateway)

	wantSubstrings := []struct{ name, cmd string }{
		{".kiro/settings/mcp.json", "ai-orch mcp install --client kiro"},
		{".kiro/hooks", "ai-orch mcp install --client kiro"},
	}
	for _, want := range wantSubstrings {
		found := false
		for _, issue := range issues {
			if strings.Contains(issue, want.name) && strings.Contains(issue, want.cmd) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected an issue naming %q and %q, got: %v", want.name, want.cmd, issues)
		}
	}
}
