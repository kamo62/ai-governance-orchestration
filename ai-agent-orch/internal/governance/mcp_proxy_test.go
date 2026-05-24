package governance

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
		Registrations: map[string]MCPProxyRegistration{
			"docs": {
				Endpoint:      backend.URL,
				AuthMode:      "oauth-user",
				PlatformToken: "platform-token",
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
