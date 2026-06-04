package governance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/openrouter"
)

func TestModelProxyForwardsWithProviderKeyAndAuditsHashes(t *testing.T) {
	var gotAuth string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"gen_1","model":"openai/gpt-5.5-20260423","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer provider.Close()

	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	handler := NewModelProxyHandler(ModelProxyConfig{
		ServiceToken: "service-token",
		OpenRouter: openrouter.NewClient(openrouter.Config{
			APIKey:  "provider-key",
			BaseURL: provider.URL,
		}),
		Audit: auditStore,
	})

	body := `{"model":"openai/gpt-5.5","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/model/chat", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_proxy")
	req.Header.Set("X-AI-Orch-Model-Alias", "coding-gpt55")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer provider-key" {
		t.Fatalf("provider auth = %q", gotAuth)
	}

	events, err := auditStore.EventsBySession(context.Background(), "sess_proxy")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	event := events[0]
	if event.EventType != "model.proxy_call" {
		t.Fatalf("event type = %q", event.EventType)
	}
	if event.ProxyCallID == "" || event.RequestSHA256 == "" || event.ResponseSHA256 == "" {
		t.Fatalf("expected proxy ids and hashes: %#v", event)
	}
	if event.ModelAlias != "coding-gpt55" || event.ModelResolved != "openai/gpt-5.5" {
		t.Fatalf("unexpected model audit fields: %#v", event)
	}
	if event.GatewayBackend != "native-openrouter" {
		t.Fatalf("expected native-openrouter gateway backend, got %q", event.GatewayBackend)
	}
	if event.TrustLevel != "gateway_enforced" {
		t.Fatalf("expected gateway_enforced trust level, got %q", event.TrustLevel)
	}
}

func TestModelProxyRejectsMissingServiceToken(t *testing.T) {
	handler := NewModelProxyHandler(ModelProxyConfig{ServiceToken: "service-token"})

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/model/chat", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestProxyClientSendsSessionHeadersWithoutProviderKey(t *testing.T) {
	var gotAuth string
	var gotSession string
	var gotAlias string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSession = r.Header.Get("X-AI-Orch-Session-ID")
		gotAlias = r.Header.Get("X-AI-Orch-Model-Alias")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(openrouter.ChatCompletionResponse{
			ID:    "gen_proxy",
			Model: "proxy-model",
		})
	}))
	defer server.Close()

	client := openrouter.NewProxyClient(openrouter.ProxyConfig{
		BaseURL:      server.URL,
		ServiceToken: "service-token",
	})

	_, err := client.ChatCompletion(context.Background(), openrouter.ChatCompletionRequest{
		SessionID:  "sess_proxy",
		ModelAlias: "coding-gpt55",
		Model:      "openai/gpt-5.5",
		Messages:   []openrouter.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("proxy chat completion: %v", err)
	}
	if gotAuth != "Bearer service-token" {
		t.Fatalf("expected service token auth, got %q", gotAuth)
	}
	if gotSession != "sess_proxy" || gotAlias != "coding-gpt55" {
		t.Fatalf("unexpected proxy headers session=%q alias=%q", gotSession, gotAlias)
	}
}
