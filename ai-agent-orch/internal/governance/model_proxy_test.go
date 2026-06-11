package governance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/modelbackend"
	"ai-agent-orch/internal/openrouter"
)

func TestModelProxyForwardsWithProviderKeyAndAuditsHashes(t *testing.T) {
	backend := &recordingRawModelBackend{body: []byte(`{"id":"gen_1","model":"openai/gpt-5.5-20260423","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)}
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	handler := NewModelProxyHandler(ModelProxyConfig{
		ServiceToken: "service-token",
		Backend:      backend,
		Audit:        auditStore,
	})

	body := `{"model":"openai/gpt-5.5","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/model/chat", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_proxy")
	req.Header.Set("X-AI-Orch-Provider", "openrouter")
	req.Header.Set("X-AI-Orch-Model-Alias", "coding-gpt55")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.last.Model != "openai/gpt-5.5" {
		t.Fatalf("unexpected backend model %q", backend.last.Model)
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
	if event.ModelAlias != "coding-gpt55" || event.ModelResolved != "openrouter:openai/gpt-5.5" {
		t.Fatalf("unexpected model audit fields: %#v", event)
	}
	if event.GatewayBackend != "raw-test" {
		t.Fatalf("expected raw-test gateway backend, got %q", event.GatewayBackend)
	}
	if event.TrustLevel != "gateway_enforced" {
		t.Fatalf("expected gateway_enforced trust level, got %q", event.TrustLevel)
	}
	if event.EnforcementMode != "gateway" {
		t.Fatalf("expected gateway enforcement mode, got %q", event.EnforcementMode)
	}
}

func TestModelProxyUsesRawActorBoundBackend(t *testing.T) {
	backend := &recordingRawModelBackend{
		body: []byte(`{"id":"raw_1","model":"gpt-5-mini","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`),
	}
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	handler := NewModelProxyHandler(ModelProxyConfig{
		ServiceToken: "service-token",
		Backend:      backend,
		Audit:        auditStore,
		LookupSession: func(context.Context, string) (SessionRecord, error) {
			return SessionRecord{SessionID: "sess_proxy", ActorSubject: "dev@example.test"}, nil
		},
	})

	body := `{"model":"gpt-5-mini","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/model/chat", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer service-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_proxy")
	req.Header.Set("X-AI-Orch-Provider", "copilot-user")
	req.Header.Set("X-AI-Orch-Model-Alias", "copilot-gpt-5-mini")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.last.ActorSubject != "dev@example.test" || backend.last.Provider != "copilot-user" || backend.last.Model != "gpt-5-mini" {
		t.Fatalf("unexpected raw backend request: %#v", backend.last)
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_proxy")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].ModelResolved != "copilot-user:gpt-5-mini" {
		t.Fatalf("unexpected audit event: %#v", events)
	}
	if got := events[0].TokenUsage["total_tokens"]; got != float64(5) {
		t.Fatalf("expected raw usage total tokens, got %#v", events[0].TokenUsage)
	}
}

type recordingRawModelBackend struct {
	last modelbackend.RawRequest
	body []byte
}

func (b *recordingRawModelBackend) Name() string { return "raw-test" }

func (b *recordingRawModelBackend) ResolvedModel(provider string, model string) string {
	return provider + ":" + model
}

func (b *recordingRawModelBackend) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, nil
}

func (b *recordingRawModelBackend) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return nil, nil
}

func (b *recordingRawModelBackend) ChatCompletionRaw(_ context.Context, req modelbackend.RawRequest) ([]byte, error) {
	b.last = req
	return b.body, nil
}

func (b *recordingRawModelBackend) ChatCompletionStreamRaw(context.Context, modelbackend.RawRequest) (io.ReadCloser, error) {
	return nil, nil
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
