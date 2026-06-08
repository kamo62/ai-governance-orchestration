package governance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-agent-orch/internal/audit"
)

func TestMCPProxyOAuthUserMissingTokenFailsClosedWithoutPlatformFallback(t *testing.T) {
	var backendCalled bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
	}))
	defer backend.Close()

	handler := NewMCPProxyHandler(MCPProxyConfig{
		ServiceToken: "service-token",
		Audit:        audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:     mcpSessionStore(t, "sess_mcp", "documentation", "internal"),
		Registrations: map[string]MCPProxyRegistration{
			"docs": {
				Endpoint:      backend.URL,
				AuthMode:      "oauth-user",
				PlatformToken: "platform-token",
				AllowedAgents: []string{"documentation"},
				ToolAllow:     []string{"search"},
			},
		},
		UserTokens: StaticUserTokenStore{},
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/mcp/docs/tools/search", strings.NewReader(`{"query":"abc"}`))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_mcp")
	req.Header.Set("X-AI-Orch-User-ID", "local-dev")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if backendCalled {
		t.Fatal("backend must not be called when oauth-user token is missing")
	}
	if strings.Contains(rec.Body.String(), "platform-token") {
		t.Fatal("response leaked platform token")
	}
}

func TestMCPProxyOAuthUserTokenUsesSessionOwnerNotCallerHeader(t *testing.T) {
	var backendCalled bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
	}))
	defer backend.Close()

	handler := NewMCPProxyHandler(MCPProxyConfig{
		ServiceToken: "service-token",
		Audit:        audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:     mcpSessionStore(t, "sess_mcp", "documentation", "internal"),
		Registrations: map[string]MCPProxyRegistration{
			"docs": {
				Endpoint:      backend.URL,
				AuthMode:      "oauth-user",
				AllowedAgents: []string{"documentation"},
				ToolAllow:     []string{"search"},
			},
		},
		UserTokens: StaticUserTokenStore{"other-user|docs": "other-token"},
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/mcp/docs/tools/search", strings.NewReader(`{"query":"abc"}`))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_mcp")
	req.Header.Set("X-AI-Orch-User-ID", "other-user")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if backendCalled {
		t.Fatal("backend must not be called with a token selected by caller-supplied user header")
	}
}

func TestMCPProxyCatalogWithDevToken(t *testing.T) {
	handler := NewMCPProxyHandler(MCPProxyConfig{
		ServiceToken: "service-token",
		DevToken:     "dev-token",
		Audit:        audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:     mcpSessionStore(t, "sess_mcp", "unit-tests", "internal"),
		Registrations: map[string]MCPProxyRegistration{
			"repo-classification": {Endpoint: "http://localhost:8091", AuthMode: "platform", AllowedAgents: []string{"unit-tests"}, ToolAllow: []string{"getRepoClassification"}},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/internal/v1/mcp/catalog", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_mcp")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "repo-classification") {
		t.Fatalf("expected repo-classification in catalog, got: %s", rec.Body.String())
	}
}

func TestMCPProxyForwardsDirectPath(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer backend.Close()

	handler := NewMCPProxyHandler(MCPProxyConfig{
		ServiceToken: "service-token",
		Audit:        audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:     mcpSessionStore(t, "sess_mcp", "documentation", "internal"),
		Registrations: map[string]MCPProxyRegistration{
			"docs": {Endpoint: backend.URL, AuthMode: "none", AllowedAgents: []string{"documentation"}, ToolAllow: []string{"getPage"}},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/mcp/docs/tools/getPage", strings.NewReader(`{"title":"home"}`))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_mcp")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/getPage" {
		t.Fatalf("expected backend path /getPage, got %q", gotPath)
	}
}

func TestMCPProxyRejectsUnknownServer(t *testing.T) {
	handler := NewMCPProxyHandler(MCPProxyConfig{
		ServiceToken: "service-token",
		Audit:        audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:     mcpSessionStore(t, "sess_mcp", "documentation", "internal"),
		Registrations: map[string]MCPProxyRegistration{
			"known": {Endpoint: "http://localhost:1", AuthMode: "none"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/mcp/unknown/tools/x", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_mcp")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMCPProxyRequiresDurableSessionBeforeForwarding(t *testing.T) {
	var backendCalled bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer backend.Close()

	handler := NewMCPProxyHandler(MCPProxyConfig{
		ServiceToken: "service-token",
		Audit:        audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:     mcpEmptySessionStore(t),
		Registrations: map[string]MCPProxyRegistration{
			"docs": {Endpoint: backend.URL, AuthMode: "none", AllowedAgents: []string{"documentation"}, ToolAllow: []string{"getPage"}},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/mcp/docs/tools/getPage", strings.NewReader(`{"title":"home"}`))
	req.Header.Set("Authorization", "Bearer service-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if backendCalled {
		t.Fatal("backend must not be called when session id is missing")
	}
}

func TestMCPProxyDeniesToolOutsideAllowListBeforeForwarding(t *testing.T) {
	var backendCalled bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
	}))
	defer backend.Close()

	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	handler := NewMCPProxyHandler(MCPProxyConfig{
		ServiceToken: "service-token",
		Audit:        auditStore,
		Sessions:     mcpSessionStore(t, "sess_mcp", "documentation", "internal"),
		Registrations: map[string]MCPProxyRegistration{
			"docs": {Endpoint: backend.URL, AuthMode: "none", AllowedAgents: []string{"documentation"}, ToolAllow: []string{"getPage"}},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/mcp/docs/tools/deletePage", strings.NewReader(`{"title":"home"}`))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_mcp")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if backendCalled {
		t.Fatal("backend must not be called when tool is denied")
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_mcp")
	if err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	if len(events) != 1 || events[0].Reason != "tool_call_denied" {
		t.Fatalf("expected tool_call_denied audit event, got %#v", events)
	}
	if events[0].TrustLevel != "gateway_enforced" || events[0].EnforcementMode != "gateway" {
		t.Fatalf("expected gateway-enforced MCP audit event, got %#v", events[0])
	}
}

func TestMCPProxyDeniesAgentNotAllowedBeforeForwarding(t *testing.T) {
	var backendCalled bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
	}))
	defer backend.Close()

	handler := NewMCPProxyHandler(MCPProxyConfig{
		ServiceToken: "service-token",
		Audit:        audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:     mcpSessionStore(t, "sess_mcp", "code-review", "internal"),
		Registrations: map[string]MCPProxyRegistration{
			"playwright-cli": {Endpoint: backend.URL, AuthMode: "none", AllowedAgents: []string{"unit-tests"}, ToolAllow: []string{"runPlaywrightTest"}},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/mcp/playwright-cli/tools/runPlaywrightTest", strings.NewReader(`{"path":"tests"}`))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_mcp")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if backendCalled {
		t.Fatal("backend must not be called when agent is denied")
	}
}

func TestMCPProxyCatalogFiltersBySessionPolicy(t *testing.T) {
	handler := NewMCPProxyHandler(MCPProxyConfig{
		ServiceToken: "service-token",
		DevToken:     "dev-token",
		Audit:        audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:     mcpSessionStore(t, "sess_mcp", "documentation", "internal"),
		Registrations: map[string]MCPProxyRegistration{
			"docs":           {Endpoint: "http://localhost:8096", AuthMode: "none", AllowedAgents: []string{"documentation"}, ToolAllow: []string{"getPage", "searchPages"}},
			"playwright-cli": {Endpoint: "http://localhost:8094", AuthMode: "none", AllowedAgents: []string{"unit-tests"}, ToolAllow: []string{"runPlaywrightTest"}},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/internal/v1/mcp/catalog", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_mcp")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "getPage") {
		t.Fatalf("expected allowed docs tools, got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "runPlaywrightTest") || strings.Contains(rec.Body.String(), "playwright-cli") {
		t.Fatalf("expected disallowed playwright server to be filtered, got: %s", rec.Body.String())
	}
}

func mcpSessionStore(t *testing.T, sessionID, agent, classification string) SessionStore {
	t.Helper()
	store := mcpEmptySessionStore(t)
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      sessionID,
		ActorSubject:   "local-dev",
		Agent:          agent,
		Classification: classification,
		PromptSHA256:   "sha",
		Status:         "created",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return store
}

func mcpEmptySessionStore(t *testing.T) *SQLiteSessionStore {
	t.Helper()
	store, err := NewSQLiteSessionStore(":memory:")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	return store
}
