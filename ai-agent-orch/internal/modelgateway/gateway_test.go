package modelgateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/modelbackend"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

type fakeChatClient struct {
	resp        openrouter.ChatCompletionResponse
	err         error
	lastRequest openrouter.ChatCompletionRequest
}

func (f *fakeChatClient) Name() string {
	return "test-backend"
}

func (f *fakeChatClient) ResolvedModel(_ string, model string) string {
	return model
}

func (f *fakeChatClient) ChatCompletion(_ context.Context, req openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	f.lastRequest = req
	return f.resp, f.err
}

func (f *fakeChatClient) ChatCompletionStream(_ context.Context, req openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	f.lastRequest = req
	return nil, f.err
}

type rawFakeBackend struct {
	lastRaw modelbackend.RawRequest
	chat    []byte
	resp    []byte
	stream  string
	err     error
}

func (f *rawFakeBackend) Name() string { return "raw-test-backend" }

func (f *rawFakeBackend) ResolvedModel(provider string, model string) string {
	if provider == "" {
		return model
	}
	return provider + "/" + model
}

func (f *rawFakeBackend) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, f.err
}

func (f *rawFakeBackend) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return nil, f.err
}

func (f *rawFakeBackend) ChatCompletionRaw(_ context.Context, req modelbackend.RawRequest) ([]byte, error) {
	f.lastRaw = req
	return f.chat, f.err
}

func (f *rawFakeBackend) ChatCompletionStreamRaw(_ context.Context, req modelbackend.RawRequest) (io.ReadCloser, error) {
	f.lastRaw = req
	return io.NopCloser(strings.NewReader(f.stream)), f.err
}

func (f *rawFakeBackend) ResponsesRaw(_ context.Context, req modelbackend.RawRequest) ([]byte, error) {
	f.lastRaw = req
	return f.resp, f.err
}

func (f *rawFakeBackend) ResponsesStreamRaw(_ context.Context, req modelbackend.RawRequest) (io.ReadCloser, error) {
	f.lastRaw = req
	return io.NopCloser(strings.NewReader(f.stream)), f.err
}

func newTestGateway() *Gateway {
	return newTestGatewayWithValidator(nil)
}

func newTestGatewayWithValidator(validate func(context.Context, string) error) *Gateway {
	return NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
				{Alias: "coding-fast", Provider: "openrouter", ModelID: "x-ai/grok-build-0.1", AllowedClassifications: []string{"public", "internal"}},
			},
		}, nil),
		Backend: &fakeChatClient{
			resp: openrouter.ChatCompletionResponse{
				ID:    "chatcmpl-test",
				Model: "anthropic/claude-opus-4.7",
				Choices: []struct {
					Message openrouter.Message `json:"message"`
				}{
					{Message: openrouter.Message{Role: "assistant", Content: "Hello"}},
				},
				Usage: openrouter.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		},
		Audit:           audit.NewFileStore(""),
		NewID:           func(prefix string) string { return prefix + "_test" },
		ValidateSession: validate,
	})
}

func TestGatewayModelsRequiresAuth(t *testing.T) {
	g := newTestGateway()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestGatewayModelsListsAliases(t *testing.T) {
	g := newTestGateway()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Data) != 2 {
		t.Fatalf("expected 2 models, got %d", len(result.Data))
	}
}

func TestGatewayChatCompletionsRequiresAuth(t *testing.T) {
	g := newTestGateway()
	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestGatewayChatCompletionsSuccess(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &fakeChatClient{
		resp: openrouter.ChatCompletionResponse{
			ID:    "chatcmpl-test",
			Model: "anthropic/claude-opus-4.7",
			Choices: []struct {
				Message openrouter.Message `json:"message"`
			}{
				{Message: openrouter.Message{Role: "assistant", Content: "Hello"}},
			},
			Usage: openrouter.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	g := newTestGatewayWithBackend(backend, auditStore)
	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_model_gateway")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result openAIChatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Model != "coding-primary" {
		t.Fatalf("expected alias in response model field, got %q", result.Model)
	}
	if len(result.Choices) != 1 || result.Choices[0].Message.Content != "Hello" {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if backend.lastRequest.Provider != "openrouter" {
		t.Fatalf("expected provider openrouter in backend request, got %q", backend.lastRequest.Provider)
	}

	events, err := auditStore.EventsBySession(context.Background(), "sess_model_gateway")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].GatewayBackend != "test-backend" {
		t.Fatalf("expected gateway backend audit metadata, got %#v", events[0])
	}
	if events[0].TrustLevel != "gateway_enforced" {
		t.Fatalf("expected gateway_enforced trust level, got %q", events[0].TrustLevel)
	}
	if events[0].EnforcementMode != "gateway" {
		t.Fatalf("expected gateway enforcement mode, got %q", events[0].EnforcementMode)
	}
	if got := numericAuditValue(events[0].TokenUsage["total_tokens"]); got != 15 {
		t.Fatalf("expected token usage in audit metadata, got %#v", events[0].TokenUsage)
	}
}

func TestGatewayChatCompletionsPreservesOpenAIToolPayload(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		chat: []byte(`{"id":"chatcmpl-test","model":"openrouter/upstream-model","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{}"}}]}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "upstream-model", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", ActorSubject: "dev@example.com"}, nil
		},
	})
	body := []byte(`{"model":"coding-primary","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"ok"},{"role":"user","content":[{"type":"text","text":"continue"}]}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}],"tool_choice":"auto","response_format":{"type":"json_object"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_model_gateway")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.lastRaw.Model != "upstream-model" || backend.lastRaw.Provider != "openrouter" {
		t.Fatalf("unexpected raw routing request: %#v", backend.lastRaw)
	}
	if !bytes.Contains(backend.lastRaw.Body, []byte(`"tool_calls"`)) || !bytes.Contains(backend.lastRaw.Body, []byte(`"response_format"`)) {
		t.Fatalf("expected raw request body to preserve tools and response_format: %s", string(backend.lastRaw.Body))
	}
	if !strings.Contains(rec.Body.String(), `"model":"coding-primary"`) {
		t.Fatalf("expected alias model in response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tool_calls") {
		t.Fatalf("expected tool_calls in response, got %s", rec.Body.String())
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_model_gateway")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].Actor != "dev@example.com" {
		t.Fatalf("expected actor-bound audit event, got %#v", events)
	}
}

func TestGatewayChatCompletionsNormalizesReasoningEffortForBifrost(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		chat: []byte(`{"id":"chatcmpl-test","model":"openrouter/openai/gpt-5.5","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"completion_tokens_details":{"reasoning_tokens":2}}}`),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{
				Alias:                  "coding-gpt55",
				Provider:               "openrouter",
				ModelID:                "openai/gpt-5.5",
				AllowedClassifications: []string{"public", "internal"},
				Routes: []catalog.ModelRoute{
					{
						Provider:         "openrouter",
						ModelID:          "openai/gpt-5.5",
						CredentialSource: "platform-openrouter",
						Reasoning: catalog.ReasoningMetadata{
							DefaultEffort:  "medium",
							MaxEffort:      "high",
							SupportsEffort: boolPtr(true),
						},
					},
				},
			},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", ActorSubject: "dev@example.test", Agent: "backend-development", Status: "running"}, nil
		},
	})
	body := []byte(`{"model":"coding-gpt55","messages":[{"role":"user","content":"hello"}],"reasoningEffort":"high"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_reasoning")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var upstream map[string]any
	if err := json.Unmarshal(backend.lastRaw.Body, &upstream); err != nil {
		t.Fatalf("decode upstream request: %v", err)
	}
	if _, ok := upstream["reasoningEffort"]; ok {
		t.Fatalf("expected OpenCode reasoningEffort field removed, got %s", string(backend.lastRaw.Body))
	}
	reasoning, ok := upstream["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("expected Bifrost reasoning effort high, got %s", string(backend.lastRaw.Body))
	}

	events, err := auditStore.EventsBySession(context.Background(), "sess_reasoning")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].ReasoningEffortRequested != "high" || events[0].ReasoningEffortApplied != "high" || events[0].ReasoningSource != "client" {
		t.Fatalf("unexpected reasoning audit fields: %#v", events[0])
	}
	if got := numericAuditValue(events[0].TokenUsage["reasoning_tokens"]); got != 2 {
		t.Fatalf("expected reasoning token usage, got %#v", events[0].TokenUsage)
	}
}

func TestGatewayChatCompletionsClampsLeadReasoningEffort(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		chat: []byte(`{"id":"chatcmpl-test","model":"openrouter/openai/gpt-5.5","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{
				Alias:                  "coding-gpt55",
				Provider:               "openrouter",
				ModelID:                "openai/gpt-5.5",
				AllowedClassifications: []string{"public", "internal"},
				Reasoning: catalog.ReasoningMetadata{
					DefaultEffort:  "medium",
					MaxEffort:      "high",
					SupportsEffort: boolPtr(true),
				},
			},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", ActorSubject: "dev@example.test", Agent: "governance-lead", Status: "running"}, nil
		},
	})
	body := []byte(`{"model":"coding-gpt55","messages":[{"role":"user","content":"hello"}],"reasoning":{"effort":"high"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_clamped")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var upstream map[string]any
	if err := json.Unmarshal(backend.lastRaw.Body, &upstream); err != nil {
		t.Fatalf("decode upstream request: %v", err)
	}
	reasoning := upstream["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" {
		t.Fatalf("expected governance-lead max medium, got %s", string(backend.lastRaw.Body))
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_clamped")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].ReasoningEffortRequested != "high" || events[0].ReasoningEffortApplied != "medium" || events[0].ReasoningSource != "policy_clamped" {
		t.Fatalf("unexpected reasoning audit fields: %#v", events[0])
	}
}

func TestGatewayChatCompletionsStripsReasoningForUnsupportedRoute(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		chat: []byte(`{"id":"chatcmpl-test","model":"gpt-5.5","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{
				Alias:                  "copilot-gpt-5.5",
				Provider:               "copilot-user",
				ModelID:                "gpt-5.5",
				AllowedClassifications: []string{"public", "internal"},
				Reasoning: catalog.ReasoningMetadata{
					DefaultEffort:  "low",
					MaxEffort:      "medium",
					SupportsEffort: boolPtr(false),
				},
			},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", ActorSubject: "dev@example.test", Agent: "governance-lead", Status: "running"}, nil
		},
	})
	body := []byte(`{"model":"copilot-gpt-5.5","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_unsupported_reasoning")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(backend.lastRaw.Body, []byte("reasoning")) {
		t.Fatalf("expected reasoning fields stripped for unsupported route, got %s", string(backend.lastRaw.Body))
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_unsupported_reasoning")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].ReasoningEffortRequested != "high" || events[0].ReasoningEffortApplied != "" || events[0].ReasoningSource != "provider_default" {
		t.Fatalf("unexpected reasoning audit fields: %#v", events[0])
	}
}

func TestGatewayChatCompletionsRequiresSessionID(t *testing.T) {
	g := newTestGateway()
	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "X-AI-Orch-Session-ID") {
		t.Fatalf("expected session header error, got: %s", rec.Body.String())
	}
}

func TestGatewayChatCompletionsAutoCreatesSession(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &fakeChatClient{
		resp: openrouter.ChatCompletionResponse{
			ID: "chatcmpl-auto",
			Choices: []struct {
				Message openrouter.Message `json:"message"`
			}{
				{Message: openrouter.Message{Role: "assistant", Content: "Hello"}},
			},
			Usage: openrouter.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		},
	}
	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		AutoSession: func(_ context.Context, req AutoSessionRequest) (SessionInfo, error) {
			if req.ActorSubject != "dev@example.test" || req.ModelAlias != "coding-primary" || req.Endpoint != "chat.completions" {
				t.Fatalf("unexpected auto session request: %#v", req)
			}
			if req.Intent != "Need direct model exploration before choosing a specialist" {
				t.Fatalf("expected intent reason, got %q", req.Intent)
			}
			if !bytes.Equal(req.RawRequestBody, body) {
				t.Fatalf("expected raw request body passed to auto session")
			}
			return SessionInfo{SessionID: "sess_auto", ActorSubject: req.ActorSubject, Classification: req.Classification, Status: "running", GatewayToken: "sgt_auto"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Actor-Subject", "dev@example.test")
	req.Header.Set("X-AI-Orch-Intent", "Need direct model exploration before choosing a specialist")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-AI-Orch-Session-ID") != "sess_auto" || rec.Header().Get("X-AI-Orch-Session-Token") != "sgt_auto" {
		t.Fatalf("expected auto-session headers, got session=%q token=%q", rec.Header().Get("X-AI-Orch-Session-ID"), rec.Header().Get("X-AI-Orch-Session-Token"))
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_auto")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "model.gateway_call" || events[0].Actor != "dev@example.test" {
		t.Fatalf("unexpected audit events: %#v", events)
	}
}

func TestGatewayAutoSessionReturnedTokenBindsSubsequentCalls(t *testing.T) {
	tokenHash := sha256.Sum256([]byte("sgt_auto"))
	lookup := map[string]SessionInfo{}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: &fakeChatClient{resp: openrouter.ChatCompletionResponse{ID: "chatcmpl-test", Choices: []struct {
			Message openrouter.Message `json:"message"`
		}{{Message: openrouter.Message{Role: "assistant", Content: "ok"}}}}},
		AutoSession: func(_ context.Context, req AutoSessionRequest) (SessionInfo, error) {
			info := SessionInfo{SessionID: "sess_auto_bound", ActorSubject: req.ActorSubject, Classification: "internal", Status: "running", GatewayTokenSHA256: hex.EncodeToString(tokenHash[:]), GatewayToken: "sgt_auto"}
			lookup[info.SessionID] = info
			return info, nil
		},
		LookupSession: func(_ context.Context, sessionID string) (SessionInfo, error) {
			info, ok := lookup[sessionID]
			if !ok {
				return SessionInfo{}, errors.New("missing")
			}
			return info, nil
		},
	})
	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	autoReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	autoReq.Header.Set("Authorization", "Bearer runtime-test-token")
	autoReq.Header.Set("X-AI-Orch-Actor-Subject", "dev@example.test")
	autoRec := httptest.NewRecorder()
	g.Handler().ServeHTTP(autoRec, autoReq)
	if autoRec.Code != http.StatusOK {
		t.Fatalf("expected auto call 200, got %d: %s", autoRec.Code, autoRec.Body.String())
	}

	missingTokenReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	missingTokenReq.Header.Set("Authorization", "Bearer runtime-test-token")
	missingTokenReq.Header.Set("X-AI-Orch-Session-ID", "sess_auto_bound")
	missingTokenRec := httptest.NewRecorder()
	g.Handler().ServeHTTP(missingTokenRec, missingTokenReq)
	if missingTokenRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing token 401, got %d: %s", missingTokenRec.Code, missingTokenRec.Body.String())
	}

	boundReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	boundReq.Header.Set("Authorization", "Bearer runtime-test-token")
	boundReq.Header.Set("X-AI-Orch-Session-ID", "sess_auto_bound")
	boundReq.Header.Set("X-AI-Orch-Session-Token", autoRec.Header().Get("X-AI-Orch-Session-Token"))
	boundRec := httptest.NewRecorder()
	g.Handler().ServeHTTP(boundRec, boundReq)
	if boundRec.Code != http.StatusOK {
		t.Fatalf("expected token-bound call 200, got %d: %s", boundRec.Code, boundRec.Body.String())
	}
}

func TestGatewayChatCompletionsRejectsUnknownSessionID(t *testing.T) {
	g := newTestGatewayWithValidator(func(context.Context, string) error {
		return errors.New("session not found")
	})
	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_missing")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGatewayChatCompletionsMissingModel(t *testing.T) {
	g := newTestGateway()
	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGatewayChatCompletionsInvalidAlias(t *testing.T) {
	g := newTestGateway()
	body := []byte(`{"model":"unknown-alias","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_model_gateway")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGatewayChatCompletionsIgnoresCallerClassificationHeader(t *testing.T) {
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "public-only", Provider: "openrouter", ModelID: "m-public", AllowedClassifications: []string{"public"}},
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "m-internal", AllowedClassifications: []string{"internal"}},
			},
		}, nil),
		Backend: &fakeChatClient{},
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal"}, nil
		},
	})

	body := []byte(`{"model":"public-only","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_internal")
	req.Header.Set("X-AI-Orch-Classification", "public")
	rec := httptest.NewRecorder()

	g.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when session classification disallows alias, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGatewayResponsesSuccess(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		resp: []byte(`{"id":"resp-test","model":"openrouter/upstream-model","output":[{"type":"message","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":10,"output_tokens":5}}`),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "upstream-model", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})
	body := []byte(`{"model":"coding-primary","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_model_gateway")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["model"] != "coding-primary" {
		t.Fatalf("expected alias in response model field, got %#v", result["model"])
	}
	if !bytes.Contains(backend.lastRaw.Body, []byte(`"tools"`)) {
		t.Fatalf("expected responses tools to be preserved, got %s", string(backend.lastRaw.Body))
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_model_gateway")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].EventType != "model.gateway_responses" {
		t.Fatalf("expected responses audit event, got %q", events[0].EventType)
	}
	if events[0].EnforcementMode != "gateway" {
		t.Fatalf("expected gateway enforcement mode, got %q", events[0].EnforcementMode)
	}
	if got := numericAuditValue(events[0].TokenUsage["total_tokens"]); got != 15 {
		t.Fatalf("expected token usage in audit metadata, got %#v", events[0].TokenUsage)
	}
}

func TestGatewayResponsesAcceptsStringInput(t *testing.T) {
	backend := &fakeChatClient{
		resp: openrouter.ChatCompletionResponse{
			ID:    "chatcmpl-test",
			Model: "anthropic/claude-opus-4.7",
			Choices: []struct {
				Message openrouter.Message `json:"message"`
			}{
				{Message: openrouter.Message{Role: "assistant", Content: "Hello"}},
			},
			Usage: openrouter.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}
	g := newTestGatewayWithBackend(backend, audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")))
	body := []byte(`{"model":"coding-primary","input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_model_gateway")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(backend.lastRequest.Messages) != 1 {
		t.Fatalf("expected one upstream message, got %#v", backend.lastRequest.Messages)
	}
	if got := backend.lastRequest.Messages[0]; got.Role != "user" || got.Content != "hello" {
		t.Fatalf("expected string input to become user message, got %#v", got)
	}
}

func TestGatewayResponsesRequiresSessionID(t *testing.T) {
	g := newTestGateway()
	body := []byte(`{"model":"coding-primary","input":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "X-AI-Orch-Session-ID") {
		t.Fatalf("expected session header error, got: %s", rec.Body.String())
	}
}

func TestGatewayStreamTranslatesChunks(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	streamClient := &streamFakeClient{}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "m1", AllowedClassifications: []string{"public", "internal"}},
			},
		}, nil),
		Backend: streamClient,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})

	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_model_gateway")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("expected [DONE] terminator in stream, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "coding-primary") {
		t.Fatalf("expected alias in stream chunks, got: %s", rec.Body.String())
	}
	if !streamClient.lastRequest.Stream {
		t.Fatal("expected upstream stream request to set stream=true")
	}
	if streamClient.lastRequest.StreamOptions == nil || !streamClient.lastRequest.StreamOptions.IncludeUsage {
		t.Fatalf("expected upstream stream request to ask for usage, got %#v", streamClient.lastRequest.StreamOptions)
	}
	if !strings.Contains(rec.Body.String(), `"prompt_tokens":12`) || !strings.Contains(rec.Body.String(), `"completion_tokens":4`) || !strings.Contains(rec.Body.String(), `"total_tokens":16`) {
		t.Fatalf("expected streamed usage frame, got: %s", rec.Body.String())
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_model_gateway")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one stream completion audit event, got %d", len(events))
	}
	if events[0].EventType != "model.gateway_stream.completed" {
		t.Fatalf("expected stream completion audit, got %q", events[0].EventType)
	}
	if events[0].ResponseSHA256 == "" {
		t.Fatal("expected response hash on stream completion audit")
	}
	if got := numericAuditValue(events[0].TokenUsage["prompt_tokens"]); got != 12 {
		t.Fatalf("expected prompt tokens in stream audit, got %#v", events[0].TokenUsage)
	}
	if got := numericAuditValue(events[0].TokenUsage["completion_tokens"]); got != 4 {
		t.Fatalf("expected completion tokens in stream audit, got %#v", events[0].TokenUsage)
	}
	if got := floatAuditValue(events[0].TokenUsage["cost_usd"]); got != 0.00002 {
		t.Fatalf("expected cost in stream audit, got %#v", events[0].TokenUsage)
	}
}

type streamFakeClient struct {
	lastRequest openrouter.ChatCompletionRequest
}

func (s *streamFakeClient) Name() string { return "stream-test" }

func (s *streamFakeClient) ResolvedModel(_ string, model string) string { return model }

func newTestGatewayWithBackend(backend *fakeChatClient, auditStore audit.Store) *Gateway {
	return NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
				{Alias: "coding-fast", Provider: "openrouter", ModelID: "x-ai/grok-build-0.1", AllowedClassifications: []string{"public", "internal"}},
			},
		}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})
}

func (s *streamFakeClient) ChatCompletion(_ context.Context, _ openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, nil
}

func (s *streamFakeClient) ChatCompletionStream(_ context.Context, req openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	s.lastRequest = req
	data := `data: {"id":"chunk1","object":"chat.completion.chunk","model":"m1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}` + "\n\n" +
		`data: {"id":"chunk-usage","object":"chat.completion.chunk","model":"m1","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16,"cost":"0.00002"}}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	return io.NopCloser(strings.NewReader(data)), nil
}

func numericAuditValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func floatAuditValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func TestGatewaySessionTokenBinding(t *testing.T) {
	tokenHash := sha256.Sum256([]byte("sgt_secret"))
	gateway := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
			},
		}, nil),
		Backend: &fakeChatClient{
			resp: openrouter.ChatCompletionResponse{
				ID: "chatcmpl-test",
				Choices: []struct {
					Message openrouter.Message `json:"message"`
				}{
					{Message: openrouter.Message{Role: "assistant", Content: "Hello"}},
				},
			},
		},
		Audit: audit.NewFileStore(""),
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", GatewayTokenSHA256: hex.EncodeToString(tokenHash[:])}, nil
		},
	})

	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	cases := []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "missing token", token: "", wantStatus: http.StatusUnauthorized},
		{name: "wrong token", token: "sgt_wrong", wantStatus: http.StatusUnauthorized},
		{name: "correct token", token: "sgt_secret", wantStatus: http.StatusOK},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer runtime-test-token")
		req.Header.Set("X-AI-Orch-Session-ID", "sess_bound")
		if tc.token != "" {
			req.Header.Set("X-AI-Orch-Session-Token", tc.token)
		}
		rec := httptest.NewRecorder()
		gateway.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.wantStatus {
			t.Fatalf("%s: expected %d, got %d: %s", tc.name, tc.wantStatus, rec.Code, rec.Body.String())
		}
	}
}

func TestGatewaySessionWithoutTokenHashStillWorks(t *testing.T) {
	gateway := newTestGatewayWithLookup(func(context.Context, string) (SessionInfo, error) {
		return SessionInfo{Classification: "internal"}, nil
	})
	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_legacy")
	rec := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected legacy session without token hash to pass, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGatewayRejectsTerminalSessionStatus(t *testing.T) {
	gateway := newTestGatewayWithLookup(func(context.Context, string) (SessionInfo, error) {
		return SessionInfo{Classification: "internal", Status: "done"}, nil
	})
	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_done")
	rec := httptest.NewRecorder()
	gateway.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected terminal session to be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func newTestGatewayWithLookup(lookup func(context.Context, string) (SessionInfo, error)) *Gateway {
	return NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
			},
		}, nil),
		Backend: &fakeChatClient{
			resp: openrouter.ChatCompletionResponse{
				ID: "chatcmpl-test",
				Choices: []struct {
					Message openrouter.Message `json:"message"`
				}{
					{Message: openrouter.Message{Role: "assistant", Content: "Hello"}},
				},
			},
		},
		Audit:         audit.NewFileStore(""),
		LookupSession: lookup,
	})
}

func boolPtr(v bool) *bool {
	return &v
}

func TestGatewayResponsesStreamIncompleteIsNotAuditedAsCompleted(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		stream: "event: response.created\n" +
			"data: {\"type\":\"response.created\"}\n" +
			"\n" +
			"event: response.incomplete\n" +
			"data: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n" +
			"\n",
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "upstream-model", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})
	body := []byte(`{"model":"coding-primary","stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_resp_incomplete")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	events, err := auditStore.EventsBySession(context.Background(), "sess_resp_incomplete")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].EventType != "model.gateway_responses_stream.incomplete" {
		t.Fatalf("expected incomplete audit event, got %q", events[0].EventType)
	}
}
