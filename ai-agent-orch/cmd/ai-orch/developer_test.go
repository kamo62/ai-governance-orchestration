package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/quick"
	"time"
)

// credServer stands up a stub /v1/developer/runtime-credential endpoint that
// echoes the requested client into capturedClient and returns the supplied
// actor/token. cfg points GovernanceURL+ModelGatewayURL at the server.
type credServer struct {
	server         *httptest.Server
	capturedClient string
}

func newCredServer(t *testing.T, actor, token string, expires time.Time) (*credServer, Config) {
	t.Helper()
	cs := &credServer{}
	cs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/developer/runtime-credential" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]string
		_ = json.Unmarshal(body, &parsed)
		cs.capturedClient = parsed["client"]
		resp, _ := json.Marshal(map[string]any{
			"actor_subject": actor,
			"runtime_token": token,
			"credential_id": "cred-1",
			"expires_at":    expires.Format(time.RFC3339),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	t.Cleanup(cs.server.Close)
	cfg := Config{GovernanceURL: cs.server.URL, ModelGatewayURL: "https://models.test.example.com"}
	return cs, cfg
}

// captureStdout runs fn with os.Stdout redirected and returns what was printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

// walkFiles returns every regular file path under root.
func walkFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

// Property 11: Unsupported clients produce a named error.
// Feature: governed-client-onboarding, Property 11
// Validates: Requirements 9.2
func TestProperty11UnsupportedClientNamedError(t *testing.T) {
	supported := map[string]bool{"opencode": true, "claude-code": true, "kiro": true}
	dir := t.TempDir()

	property := func(client string) bool {
		if supported[client] {
			return true // skip the valid clients
		}
		before := walkFiles(t, dir)
		err := developerEnroll(t.Context(), Config{}, []string{"--client", client, "--dir", dir})
		if err == nil {
			return false
		}
		// Error must name the offending client value.
		if !strings.Contains(err.Error(), fmt.Sprintf("%q", client)) {
			return false
		}
		// No side effects: nothing written.
		after := walkFiles(t, dir)
		return len(before) == len(after)
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 200}); err != nil {
		t.Fatalf("unsupported-client property failed: %v", err)
	}
}

// Property 12: Kiro enrolment configures no model override.
// Feature: governed-client-onboarding, Property 12
// Validates: Requirements 8.3, 8.4
func TestProperty12KiroNoModelOverride(t *testing.T) {
	property := func(actor, token string) bool {
		if token == "" || actor == "" {
			return true // requestDeveloperRuntimeCredential rejects empties; out of scope here
		}
		_, cfg := newCredServer(t, actor, token, time.Now().Add(24*time.Hour))
		dir := t.TempDir()
		if err := enrollKiro(t.Context(), cfg, dir, true); err != nil {
			return false
		}
		// No file may carry a model-proxy override.
		for _, f := range walkFiles(t, dir) {
			data, err := os.ReadFile(f)
			if err != nil {
				return false
			}
			if strings.Contains(string(data), "ANTHROPIC_BASE_URL") || strings.Contains(string(data), "ANTHROPIC_AUTH_TOKEN") {
				return false
			}
		}
		// The runtime token must be wired into the Kiro MCP env.
		mcp, err := os.ReadFile(filepath.Join(dir, ".kiro", "settings", "mcp.json"))
		if err != nil {
			return false
		}
		var parsed map[string]any
		if err := json.Unmarshal(mcp, &parsed); err != nil {
			return false
		}
		servers, _ := parsed["mcpServers"].(map[string]any)
		gw, _ := servers["ai-orch-gateway"].(map[string]any)
		env, _ := gw["env"].(map[string]any)
		return env["AI_ORCH_DEV_TOKEN"] == token
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatalf("kiro no-override property failed: %v", err)
	}
}

// Property 10: Only the runtime token is written; provider credentials are excluded.
// Feature: governed-client-onboarding, Property 10
// Validates: Requirements 7.6, 9.4
func TestProperty10OnlyRuntimeTokenWritten(t *testing.T) {
	property := func(token, providerKey string) bool {
		// Constrain to non-empty, distinct sentinels so the absence check is meaningful.
		if token == "" || providerKey == "" || token == providerKey {
			return true
		}
		_, cfg := newCredServer(t, "dev-user", token, time.Now().Add(24*time.Hour))
		dir := t.TempDir()
		settingsPath := filepath.Join(t.TempDir(), "settings.json")
		if err := enrollClaudeCode(t.Context(), cfg, dir, settingsPath, true); err != nil {
			return false
		}
		files := append(walkFiles(t, dir), settingsPath)
		tokenSeen := false
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				return false
			}
			content := string(data)
			// A provider key (never passed anywhere) must never appear.
			if strings.Contains(content, providerKey) {
				return false
			}
			if strings.Contains(content, token) {
				tokenSeen = true
			}
		}
		return tokenSeen
	}

	if err := quick.Check(property, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatalf("provider-credential-exclusion property failed: %v", err)
	}
}

// Edge 4.10: credential-request wiring + reported summary fields for claude-code.
// Validates: Requirements 7.1, 7.3, 7.4, 7.7, 9.1
func TestEnrollClaudeCodeWiringAndSummary(t *testing.T) {
	expires := time.Now().Add(48 * time.Hour).Round(time.Second)
	cs, cfg := newCredServer(t, "alice@example.com", "runtime-token-abc", expires)
	dir := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")

	out := captureStdout(t, func() {
		if err := enrollClaudeCode(t.Context(), cfg, dir, settingsPath, true); err != nil {
			t.Fatalf("enrollClaudeCode: %v", err)
		}
	})

	if cs.capturedClient != "claude-code" {
		t.Fatalf("expected credential request for claude-code, got %q", cs.capturedClient)
	}
	// settings.json wires gateway base URL + runtime token under env.
	var settings map[string]any
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	env, _ := settings["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != cfg.ModelGatewayURL {
		t.Fatalf("expected ANTHROPIC_BASE_URL=%q, got %v", cfg.ModelGatewayURL, env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "runtime-token-abc" {
		t.Fatalf("expected runtime token in env, got %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	// Summary reports actor subject, expiry, and backup path note.
	if !strings.Contains(out, "alice@example.com") {
		t.Fatalf("summary missing actor subject: %q", out)
	}
	if !strings.Contains(out, expires.Format(time.RFC3339)) {
		t.Fatalf("summary missing expiry: %q", out)
	}
}

// Edge 4.10: credential-request wiring + reported summary fields for kiro, and
// client acceptance for claude-code/kiro.
// Validates: Requirements 8.1, 8.2, 8.3, 8.5, 9.1
func TestEnrollKiroWiringAndSummary(t *testing.T) {
	expires := time.Now().Add(72 * time.Hour).Round(time.Second)
	cs, cfg := newCredServer(t, "bob@example.com", "runtime-token-xyz", expires)
	dir := t.TempDir()

	out := captureStdout(t, func() {
		if err := enrollKiro(t.Context(), cfg, dir, true); err != nil {
			t.Fatalf("enrollKiro: %v", err)
		}
	})

	if cs.capturedClient != "kiro" {
		t.Fatalf("expected credential request for kiro, got %q", cs.capturedClient)
	}
	if !strings.Contains(out, "bob@example.com") {
		t.Fatalf("summary missing actor subject: %q", out)
	}
	if !strings.Contains(out, expires.Format(time.RFC3339)) {
		t.Fatalf("summary missing expiry: %q", out)
	}
}

// Client acceptance (9.1): claude-code and kiro are accepted by the enrol switch.
func TestEnrollAcceptsClaudeCodeAndKiro(t *testing.T) {
	for _, client := range []string{"claude-code", "kiro"} {
		_, cfg := newCredServer(t, "dev", "tok", time.Now().Add(time.Hour))
		dir := t.TempDir()
		args := []string{"--client", client, "--dir", dir, "--force"}
		if client == "claude-code" {
			args = append(args, "--path", filepath.Join(t.TempDir(), "settings.json"))
		}
		if err := developerEnroll(t.Context(), cfg, args); err != nil {
			t.Fatalf("expected %s to be accepted, got %v", client, err)
		}
	}
}
