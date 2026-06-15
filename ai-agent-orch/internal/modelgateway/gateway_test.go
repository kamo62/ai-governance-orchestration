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

func TestGatewayModelsIncludesActorBoundCopilotPickerModels(t *testing.T) {
	backend := &dynamicCopilotCatalogBackend{
		modelsBody: []byte("{" +
			"\"data\":[" +
			"{\"id\":\"claude-opus-4.8\",\"name\":\"Claude Opus 4.8\",\"model_picker_enabled\":true,\"capabilities\":{\"type\":\"chat\",\"family\":\"claude-opus-4.8\"}}," +
			"{\"id\":\"text-embedding-3-small\",\"name\":\"Embedding\",\"model_picker_enabled\":true,\"capabilities\":{\"type\":\"embeddings\"}}" +
			"]" +
			"}"),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-fast", Provider: "openrouter", ModelID: "fast-model", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   audit.NewFileStore(""),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models?classification=internal", nil)
	req.Header.Set("Authorization", "Bearer runtime-test-token.dev-user")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := result["data"].([]any)
	ids := map[string]string{}
	for _, item := range data {
		obj, _ := item.(map[string]any)
		id, _ := obj["id"].(string)
		name, _ := obj["name"].(string)
		ids[id] = name
	}
	if _, ok := ids["coding-fast"]; !ok {
		t.Fatalf("expected static coding-fast alias, got %#v", ids)
	}
	if ids["copilot-claude-opus-4.8"] != "Governed Copilot Claude Opus 4.8" {
		t.Fatalf("expected dynamic Copilot Opus alias, got %#v", ids)
	}
	if _, ok := ids["copilot-text-embedding-3-small"]; ok {
		t.Fatalf("did not expect embedding model in picker list: %#v", ids)
	}
	if backend.modelsActor != "dev-user" {
		t.Fatalf("expected composite API key actor to drive model listing, got %q", backend.modelsActor)
	}
}

func TestGatewayDoesNotListDynamicCopilotModelsForRestrictedClassification(t *testing.T) {
	backend := &dynamicCopilotCatalogBackend{
		modelsBody: []byte("{" +
			"\"data\":[" +
			"{\"id\":\"claude-opus-4.8\",\"name\":\"Claude Opus 4.8\",\"model_picker_enabled\":true,\"capabilities\":{\"type\":\"chat\"}}" +
			"]" +
			"}"),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-fast", Provider: "openrouter", ModelID: "fast-model", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   audit.NewFileStore(""),
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/models?classification=restricted", nil)
	req.Header.Set("Authorization", "Bearer runtime-test-token.dev-user")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "copilot-claude-opus-4.8") {
		t.Fatalf("did not expect dynamic Copilot picker aliases for restricted classification: %s", rec.Body.String())
	}
	if backend.modelsActor != "" {
		t.Fatalf("did not expect Copilot catalog lookup for restricted classification, got actor %q", backend.modelsActor)
	}
}

func TestGatewayRoutesDynamicCopilotAlias(t *testing.T) {
	backend := &dynamicCopilotCatalogBackend{
		rawFakeBackend: rawFakeBackend{
			chat: []byte("{\"id\":\"chatcmpl-test\",\"model\":\"claude-opus-4.8\",\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"ok\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}"),
		},
		modelsBody: []byte("{\"data\":[{\"id\":\"claude-opus-4.8\",\"name\":\"Claude Opus 4.8\",\"model_picker_enabled\":true,\"capabilities\":{\"type\":\"chat\"}}]}"),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-fast", Provider: "openrouter", ModelID: "fast-model", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", ActorSubject: "dev-user", Status: "running"}, nil
		},
	})

	body := []byte("{\"model\":\"copilot-claude-opus-4.8\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_dynamic_copilot")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.lastRaw.Provider != modelbackend.BackendCopilotUser || backend.lastRaw.Model != "claude-opus-4.8" {
		t.Fatalf("expected dynamic Copilot route, got %#v", backend.lastRaw)
	}
	if backend.lastRaw.ModelAlias != "copilot-claude-opus-4.8" || backend.lastRaw.ActorSubject != "dev-user" {
		t.Fatalf("expected actor-bound dynamic alias, got %#v", backend.lastRaw)
	}
	if !strings.Contains(rec.Body.String(), "\"model\":\"copilot-claude-opus-4.8\"") {
		t.Fatalf("expected response model rewritten to alias, got %s", rec.Body.String())
	}
}

func TestGatewayRejectsDynamicCopilotAliasForRestrictedClassification(t *testing.T) {
	backend := &dynamicCopilotCatalogBackend{
		rawFakeBackend: rawFakeBackend{
			chat: []byte("{\"id\":\"chatcmpl-test\",\"model\":\"claude-opus-4.8\",\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"ok\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}"),
		},
		modelsBody: []byte("{\"data\":[{\"id\":\"claude-opus-4.8\",\"name\":\"Claude Opus 4.8\",\"model_picker_enabled\":true,\"capabilities\":{\"type\":\"chat\"}}]}"),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-fast", Provider: "openrouter", ModelID: "fast-model", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "restricted", ActorSubject: "dev-user", Status: "running"}, nil
		},
	})

	body := []byte("{\"model\":\"copilot-claude-opus-4.8\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_dynamic_copilot_restricted")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected restricted dynamic alias to be rejected with 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.lastRaw.Provider != "" {
		t.Fatalf("did not expect backend route for restricted dynamic alias, got %#v", backend.lastRaw)
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
		chat: []byte(`{"id":"chatcmpl-test","model":"gpt-5-mini","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{
				Alias:                  "copilot-gpt-5-mini",
				Provider:               "copilot-user",
				ModelID:                "gpt-5-mini",
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
	body := []byte(`{"model":"copilot-gpt-5-mini","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"high"}`)
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

func TestGatewayChatCompletionsEmitsFlatReasoningEffortForCopilot(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		chat: []byte(`{"id":"chatcmpl-test","model":"claude-opus-4.8","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{
				Alias:                  "copilot-claude-opus-4.8",
				Provider:               "copilot-user",
				ModelID:                "claude-opus-4.8",
				AllowedClassifications: []string{"public", "internal"},
				Reasoning: catalog.ReasoningMetadata{
					DefaultEffort:  "low",
					MaxEffort:      "xhigh",
					SupportsEffort: boolPtr(true),
				},
			},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", ActorSubject: "dev@example.test", Agent: "frontend-development", Status: "running"}, nil
		},
	})
	body := []byte(`{"model":"copilot-claude-opus-4.8","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"xhigh"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_copilot_reasoning")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(backend.lastRaw.Body, []byte(`"reasoning_effort":"xhigh"`)) {
		t.Fatalf("expected flat reasoning_effort for copilot route, got %s", string(backend.lastRaw.Body))
	}
	if bytes.Contains(backend.lastRaw.Body, []byte(`"reasoning":{`)) {
		t.Fatalf("expected no nested reasoning object for copilot route, got %s", string(backend.lastRaw.Body))
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_copilot_reasoning")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].ReasoningEffortApplied != "xhigh" || events[0].ReasoningSource != "client" {
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
	finishedSessionID := ""
	finishedStatus := ""
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		FinishAutoSession: func(_ context.Context, sessionID string, status string) error {
			finishedSessionID = sessionID
			finishedStatus = status
			return nil
		},
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
	if finishedSessionID != "sess_auto" || finishedStatus != "completed" {
		t.Fatalf("expected auto session completion, got session=%q status=%q", finishedSessionID, finishedStatus)
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_auto")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "model.gateway_call" || events[0].Actor != "dev@example.test" {
		t.Fatalf("unexpected audit events: %#v", events)
	}
}

func TestGatewayChatCompletionsAutoCreatesSessionWithUnresolvedEnvPlaceholders(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &fakeChatClient{
		resp: openrouter.ChatCompletionResponse{
			ID: "chatcmpl-auto-placeholder",
			Choices: []struct {
				Message openrouter.Message `json:"message"`
			}{
				{Message: openrouter.Message{Role: "assistant", Content: "Hello"}},
			},
			Usage: openrouter.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		},
	}
	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"hello"}]}`)
	autoSessionCalled := false
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		AutoSession: func(_ context.Context, req AutoSessionRequest) (SessionInfo, error) {
			autoSessionCalled = true
			if req.ActorSubject != "dev@example.test" || req.ModelAlias != "coding-primary" {
				t.Fatalf("unexpected auto session request: %#v", req)
			}
			return SessionInfo{SessionID: "sess_auto_placeholder", ActorSubject: req.ActorSubject, Classification: req.Classification, Status: "running", GatewayToken: "sgt_auto"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "{env:AI_ORCH_SESSION_ID}")
	req.Header.Set("X-AI-Orch-Session-Token", "{env:AI_ORCH_SESSION_TOKEN}")
	req.Header.Set("X-AI-Orch-Actor-Subject", "dev@example.test")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !autoSessionCalled {
		t.Fatal("expected unresolved session placeholders to use auto-session path")
	}
	if rec.Header().Get("X-AI-Orch-Session-ID") != "sess_auto_placeholder" {
		t.Fatalf("expected auto-session header, got %q", rec.Header().Get("X-AI-Orch-Session-ID"))
	}
}

// Regression: a successful non-streaming RAW chat call must finish the
// auto-created session as completed, not leave the deferred "failed" default.
func TestGatewayChatCompletionsAutoSessionRawBackendCompletes(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		chat: []byte(`{"id":"chatcmpl-raw","model":"claude-opus-4.8","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
	}
	body := []byte(`{"model":"copilot-claude-opus-4.8","messages":[{"role":"user","content":"hello"}]}`)
	finishedStatus := ""
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "copilot-claude-opus-4.8", Provider: "copilot-user", ModelID: "claude-opus-4.8", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		FinishAutoSession: func(_ context.Context, _ string, status string) error {
			finishedStatus = status
			return nil
		},
		AutoSession: func(_ context.Context, req AutoSessionRequest) (SessionInfo, error) {
			return SessionInfo{SessionID: "sess_auto_raw", ActorSubject: req.ActorSubject, Classification: req.Classification, Status: "running"}, nil
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Actor-Subject", "dev@example.test")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if finishedStatus != "completed" {
		t.Fatalf("expected raw auto-session to finish completed, got %q", finishedStatus)
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

func TestGatewayStreamAuditCountsResponseToolCalls(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		stream: "data: {\"id\":\"chunk1\",\"object\":\"chat.completion.chunk\",\"model\":\"m1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_task_1\",\"type\":\"function\",\"function\":{\"name\":\"task\",\"arguments\":\"\"}}]}}]}\n\n" +
			"data: {\"id\":\"chunk2\",\"object\":\"chat.completion.chunk\",\"model\":\"m1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"agent\\\":\\\"architecture-review\\\"}\"}}]}}]}\n\n" +
			"data: {\"id\":\"chunk3\",\"object\":\"chat.completion.chunk\",\"model\":\"m1\",\"choices\":[{\"index\":0,\"finish_reason\":\"tool_calls\",\"delta\":{}}]}\n\n" +
			"data: [DONE]\n\n",
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

	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"delegate this"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_tool_count")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	events, err := auditStore.EventsBySession(context.Background(), "sess_tool_count")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].ToolCallCount != 1 {
		t.Fatalf("expected streamed tool call count in audit event, got %#v", events[0])
	}
	if len(events[0].ToolCallNames) != 1 || events[0].ToolCallNames[0] != "task" {
		t.Fatalf("expected sanitized tool call name in audit event, got %#v", events[0].ToolCallNames)
	}
}

func TestGatewayNonStreamAuditCountsResponseToolCalls(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		chat: []byte(`{"id":"chatcmpl_tool","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_read_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]}}]}`),
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

	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"read this"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_nonstream_tool_count")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	events, err := auditStore.EventsBySession(context.Background(), "sess_nonstream_tool_count")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].ToolCallCount != 1 || len(events[0].ToolCallNames) != 1 || events[0].ToolCallNames[0] != "read" {
		t.Fatalf("expected non-stream tool call metadata in audit event, got %#v", events[0])
	}
}

func TestCopilotModelUsesResponsesAPIMatchesOpenCodeRouteRule(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		model    string
		want     bool
	}{
		{name: "copilot gpt 5", provider: modelbackend.BackendCopilotUser, model: "gpt-5", want: true},
		{name: "copilot gpt 5.5", provider: modelbackend.BackendCopilotUser, model: "gpt-5.5", want: true},
		{name: "copilot gpt 5 codex", provider: modelbackend.BackendCopilotUser, model: "gpt-5.3-codex", want: true},
		{name: "copilot gpt 5 mini", provider: modelbackend.BackendCopilotUser, model: "gpt-5-mini", want: false},
		{name: "copilot gpt 5 mini dated", provider: modelbackend.BackendCopilotUser, model: "gpt-5-mini-2025-08-07", want: false},
		{name: "copilot claude", provider: modelbackend.BackendCopilotUser, model: "claude-sonnet-4", want: false},
		{name: "copilot gpt 4", provider: modelbackend.BackendCopilotUser, model: "gpt-4o", want: false},
		{name: "openrouter gpt 5.5", provider: "openrouter", model: "openai/gpt-5.5", want: false},
		{name: "github copilot provider id", provider: "github-copilot", model: "gpt-5.1-codex", want: true},
		{name: "future gpt major", provider: modelbackend.BackendCopilotUser, model: "gpt-10-codex", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := copilotModelUsesResponsesAPI(tc.provider, tc.model); got != tc.want {
				t.Fatalf("copilotModelUsesResponsesAPI(%q, %q) = %v, want %v", tc.provider, tc.model, got, tc.want)
			}
		})
	}
}

func TestGatewayChatToResponsesBridgePreservesOpenAITools(t *testing.T) {
	backend := &responsesFallbackBackend{
		responsesStream: "event: response.output_text.delta\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n" +
			"\n" +
			"event: response.completed\n" +
			"data: {\"type\":\"response.completed\"}\n" +
			"\n",
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-gpt55", Provider: modelbackend.BackendCopilotUser, ModelID: "gpt-5.5", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		NewID:   func(prefix string) string { return prefix + "_test" },
	})

	body := []byte("{" +
		"\"model\":\"coding-gpt55\"," +
		"\"messages\":[" +
		"{\"role\":\"user\",\"content\":\"Inspect the project\"}," +
		"{\"role\":\"assistant\",\"content\":null,\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}]}," +
		"{\"role\":\"tool\",\"tool_call_id\":\"call_1\",\"content\":\"# README\"}" +
		"]," +
		"\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"description\":\"Read a file\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\"}}},\"strict\":true}}]," +
		"\"tool_choice\":\"auto\"," +
		"\"stream\":true" +
		"}")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_tool_bridge")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected bridge stream to return 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var bridged map[string]any
	if err := json.Unmarshal(backend.lastResponses.Body, &bridged); err != nil {
		t.Fatalf("decode bridged responses body: %v\n%s", err, string(backend.lastResponses.Body))
	}
	tools, ok := bridged["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected converted Responses tools, got %#v body=%s", bridged["tools"], string(backend.lastResponses.Body))
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "read_file" || tool["description"] != "Read a file" || tool["strict"] != true {
		t.Fatalf("expected flattened function tool, got %#v", tool)
	}
	input, ok := bridged["input"].([]any)
	if !ok {
		t.Fatalf("expected responses input, got %#v", bridged["input"])
	}
	var sawCall, sawOutput bool
	for _, item := range input {
		obj, _ := item.(map[string]any)
		if obj["type"] == "function_call" && obj["call_id"] == "call_1" && obj["name"] == "read_file" {
			sawCall = true
		}
		if obj["type"] == "function_call_output" && obj["call_id"] == "call_1" && obj["output"] == "# README" {
			sawOutput = true
		}
	}
	if !sawCall || !sawOutput {
		t.Fatalf("expected tool call and tool output turns in Responses input, got %#v", input)
	}
	if bridged["tool_choice"] != "auto" {
		t.Fatalf("expected tool_choice preserved, got %#v", bridged["tool_choice"])
	}
}

func TestGatewayChatStreamTranslatesResponsesFunctionCallsToChatToolCalls(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &responsesFallbackBackend{
		responsesStream: "event: response.output_item.added\n" +
			"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read_file\",\"arguments\":\"\"},\"output_index\":0}\n" +
			"\n" +
			"event: response.function_call_arguments.delta\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"delta\":\"{\\\"path\\\"\"}\n" +
			"\n" +
			"event: response.function_call_arguments.delta\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"delta\":\":\\\"README.md\\\"}\"}\n" +
			"\n" +
			"event: response.output_item.done\n" +
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"item_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}}\n" +
			"\n" +
			"event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":2,\"total_tokens\":7}}}\n" +
			"\n",
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-gpt55", Provider: modelbackend.BackendCopilotUser, ModelID: "gpt-5.5", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})

	body := []byte("{\"model\":\"coding-gpt55\",\"messages\":[{\"role\":\"user\",\"content\":\"read README\"}],\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"description\":\"Read a file\",\"parameters\":{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\"}}}}}],\"stream\":true}")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_responses_tool_call_bridge")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected translated tool stream to return 200, got %d: %s", rec.Code, rec.Body.String())
	}
	stream := rec.Body.String()
	for _, want := range []string{"\"tool_calls\"", "\"id\":\"call_1\"", "\"name\":\"read_file\"", "README.md", "\"finish_reason\":\"tool_calls\"", "data: [DONE]"} {
		if !strings.Contains(stream, want) {
			t.Fatalf("expected stream to contain %s, got: %s", want, stream)
		}
	}
	if got := strings.Count(stream, "\"id\":\"call_1\""); got != 1 {
		t.Fatalf("expected one tool-call start for call_1, got %d in stream: %s", got, stream)
	}
	if backend.chatCalls != 0 || backend.responsesCalls != 1 {
		t.Fatalf("expected direct Responses bridge, got chat=%d responses=%d", backend.chatCalls, backend.responsesCalls)
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_responses_tool_call_bridge")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "model.gateway_stream.completed" {
		t.Fatalf("expected completed stream audit, got %#v", events)
	}
	if got := numericAuditValue(events[0].TokenUsage["input_tokens"]); got != 5 {
		t.Fatalf("expected responses usage in translated audit, got %#v", events[0].TokenUsage)
	}
}

func TestGatewayChatStreamUsesResponsesDirectlyForCopilotGPT5ClassModel(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &responsesFallbackBackend{
		chatStream: "data: {\"choices\":[{\"delta\":{\"content\":\"chat-path\"},\"index\":0}]}\n\n",
		responsesStream: "event: response.output_text.delta\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n" +
			"\n" +
			"event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n" +
			"\n",
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-gpt55", Provider: modelbackend.BackendCopilotUser, ModelID: "gpt-5.5", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})

	body := []byte("{\"model\":\"coding-gpt55\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}],\"stream\":true}")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_copilot_direct_responses")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected direct responses stream to return 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.chatCalls != 0 {
		t.Fatalf("expected Copilot GPT-5-class chat request to skip chat completions, got %d chat calls", backend.chatCalls)
	}
	if backend.responsesCalls != 1 {
		t.Fatalf("expected one responses stream call, got %d", backend.responsesCalls)
	}
	if strings.Contains(rec.Body.String(), "chat-path") || !strings.Contains(rec.Body.String(), "\"content\":\"ok\"") {
		t.Fatalf("expected Responses SSE translated into chat chunks, got: %s", rec.Body.String())
	}
	if backend.lastResponses.Model != "gpt-5.5" {
		t.Fatalf("expected upstream Copilot model id, got %#v", backend.lastResponses)
	}
}

func TestGatewayChatNonStreamUsesResponsesForCopilotGPT5ClassModel(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &responsesFallbackBackend{
		responsesRaw: []byte(`{"id":"resp-test","model":"gpt-5.5","output":[{"type":"reasoning","content":[]},{"type":"message","content":[{"type":"output_text","text":"capability-ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`),
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-gpt55", Provider: modelbackend.BackendCopilotUser, ModelID: "gpt-5.5", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})

	body := []byte(`{"model":"coding-gpt55","messages":[{"role":"user","content":"Reply exactly: capability-ok"}],"max_tokens":32}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_copilot_nonstream_responses")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected direct responses non-stream to return 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.chatCalls != 0 {
		t.Fatalf("expected Copilot GPT-5-class non-stream request to skip chat completions, got %d chat calls", backend.chatCalls)
	}
	if backend.responsesCalls != 1 {
		t.Fatalf("expected one responses raw call, got %d", backend.responsesCalls)
	}
	if !strings.Contains(rec.Body.String(), `"object":"chat.completion"`) || !strings.Contains(rec.Body.String(), `"content":"capability-ok"`) || !strings.Contains(rec.Body.String(), `"model":"coding-gpt55"`) {
		t.Fatalf("expected Responses body translated into chat completion, got: %s", rec.Body.String())
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_copilot_nonstream_responses")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "model.gateway_call" {
		t.Fatalf("expected completed gateway call audit, got %#v", events)
	}
	if got := numericAuditValue(events[0].TokenUsage["total_tokens"]); got != 7 {
		t.Fatalf("expected translated Responses usage in audit, got %#v", events[0].TokenUsage)
	}
}

func TestGatewayChatStreamKeepsCopilotAnthropicModelsOnChatCompletions(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &responsesFallbackBackend{
		chatStream: "data: {\"choices\":[{\"delta\":{\"content\":\"chat-path\"},\"index\":0}]}\n\n" +
			"data: [DONE]\n\n",
		responsesStream: "event: response.output_text.delta\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"responses-path\"}\n" +
			"\n",
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "copilot-sonnet", Provider: modelbackend.BackendCopilotUser, ModelID: "claude-sonnet-4", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})

	body := []byte("{\"model\":\"copilot-sonnet\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}],\"stream\":true}")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_copilot_anthropic_chat")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected chat stream to return 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.chatCalls != 1 {
		t.Fatalf("expected Anthropic-through-Copilot model to use chat completions, got %d chat calls", backend.chatCalls)
	}
	if backend.responsesCalls != 0 {
		t.Fatalf("expected no responses calls for Anthropic-through-Copilot model, got %d", backend.responsesCalls)
	}
	if !strings.Contains(rec.Body.String(), "chat-path") || strings.Contains(rec.Body.String(), "responses-path") {
		t.Fatalf("expected chat stream path, got: %s", rec.Body.String())
	}
	if backend.lastChat.Model != "claude-sonnet-4" {
		t.Fatalf("expected upstream Copilot Anthropic model id, got %#v", backend.lastChat)
	}
}

func TestGatewayChatStreamFallsBackToResponsesStreamForResponsesOnlyModel(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &responsesFallbackBackend{
		chatErr: errors.New(`copilot returned 400: {"error":{"message":"model \"gpt-5.5\" is not accessible via the /chat/completions endpoint","code":"unsupported_api_for_model"}}`),
		responsesStream: "event: response.created\n" +
			"data: {\"type\":\"response.created\"}\n" +
			"\n" +
			"event: response.output_text.delta\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n" +
			"\n" +
			"event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"copilot_usage\":{\"total_nano_aiu\":56000000},\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"total_tokens\":12}}}\n" +
			"\n",
	}
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-gpt55", Provider: "copilot-user", ModelID: "gpt-5.5", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
	})

	body := []byte(`{"model":"coding-gpt55","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_responses_only_chat_client")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected fallback stream to return 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if backend.lastResponses.Model != "gpt-5.5" || !strings.Contains(string(backend.lastResponses.Body), `"input"`) {
		t.Fatalf("expected fallback to call responses stream with converted input, got %#v body=%s", backend.lastResponses, string(backend.lastResponses.Body))
	}
	if !strings.Contains(rec.Body.String(), `"object":"chat.completion.chunk"`) || !strings.Contains(rec.Body.String(), `"content":"ok"`) {
		t.Fatalf("expected Responses SSE translated into chat chunks, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("expected chat stream terminator, got: %s", rec.Body.String())
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_responses_only_chat_client")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "model.gateway_stream.completed" {
		t.Fatalf("expected translated chat stream completion audit, got %#v", events)
	}
	if got := numericAuditValue(events[0].TokenUsage["input_tokens"]); got != 10 {
		t.Fatalf("expected responses usage in translated audit, got %#v", events[0].TokenUsage)
	}
	if got := numericAuditValue(events[0].TokenUsage["copilot_nano_aiu"]); got != 56000000 {
		t.Fatalf("expected Copilot AI-unit usage in translated audit, got %#v", events[0].TokenUsage)
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

type responsesFallbackBackend struct {
	chatErr         error
	chatStream      string
	responsesRaw    []byte
	responsesStream string
	chatCalls       int
	responsesCalls  int
	lastChat        modelbackend.RawRequest
	lastResponses   modelbackend.RawRequest
}

func (b *responsesFallbackBackend) Name() string { return "responses-fallback-test" }

func (b *responsesFallbackBackend) ResolvedModel(_ string, model string) string { return model }

func (b *responsesFallbackBackend) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, b.chatErr
}

func (b *responsesFallbackBackend) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return nil, b.chatErr
}

func (b *responsesFallbackBackend) ChatCompletionRaw(_ context.Context, req modelbackend.RawRequest) ([]byte, error) {
	b.chatCalls++
	b.lastChat = req
	if b.chatErr != nil {
		return nil, b.chatErr
	}
	return []byte(b.chatStream), nil
}

func (b *responsesFallbackBackend) ChatCompletionStreamRaw(_ context.Context, req modelbackend.RawRequest) (io.ReadCloser, error) {
	b.chatCalls++
	b.lastChat = req
	if b.chatErr != nil {
		return nil, b.chatErr
	}
	return io.NopCloser(strings.NewReader(b.chatStream)), nil
}

func (b *responsesFallbackBackend) ResponsesRaw(_ context.Context, req modelbackend.RawRequest) ([]byte, error) {
	b.responsesCalls++
	b.lastResponses = req
	if len(b.responsesRaw) > 0 {
		return b.responsesRaw, nil
	}
	return []byte(`{"id":"resp-test","model":"` + req.Model + `","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`), nil
}

func (b *responsesFallbackBackend) ResponsesStreamRaw(_ context.Context, req modelbackend.RawRequest) (io.ReadCloser, error) {
	b.responsesCalls++
	b.lastResponses = req
	return io.NopCloser(strings.NewReader(b.responsesStream)), nil
}

type dynamicCopilotCatalogBackend struct {
	rawFakeBackend
	modelsBody  []byte
	modelsErr   error
	modelsActor string
}

func (b *dynamicCopilotCatalogBackend) Models(_ context.Context, actorSubject string) ([]byte, error) {
	b.modelsActor = actorSubject
	if b.modelsErr != nil {
		return nil, b.modelsErr
	}
	return b.modelsBody, nil
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

func TestGatewayResponsesStreamAuditCountsFunctionCalls(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		stream: "event: response.output_item.added\n" +
			"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"type\":\"function_call\",\"call_id\":\"call_edit_1\",\"name\":\"edit\",\"arguments\":\"\"}}\n" +
			"\n" +
			"event: response.output_item.done\n" +
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"item_1\",\"type\":\"function_call\",\"call_id\":\"call_edit_1\",\"name\":\"edit\",\"arguments\":\"{}\"}}\n" +
			"\n" +
			"event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n" +
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

	body := []byte(`{"model":"coding-primary","stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"edit"}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_resp_tool_count")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	events, err := auditStore.EventsBySession(context.Background(), "sess_resp_tool_count")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].EventType != "model.gateway_responses_stream.completed" {
		t.Fatalf("expected completed responses stream audit, got %q", events[0].EventType)
	}
	if events[0].ToolCallCount != 1 || len(events[0].ToolCallNames) != 1 || events[0].ToolCallNames[0] != "edit" {
		t.Fatalf("expected responses stream tool call metadata in audit event, got %#v", events[0])
	}
}

func TestGatewayStreamCreatesTaskDelegationFromOpenCodeToolCall(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		stream: "data: {\"id\":\"chunk1\",\"object\":\"chat.completion.chunk\",\"model\":\"m1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_task_1\",\"type\":\"function\",\"function\":{\"name\":\"task\",\"arguments\":\"\"}}]}}]}\n\n" +
			"data: {\"id\":\"chunk2\",\"object\":\"chat.completion.chunk\",\"model\":\"m1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"subagent_type\\\":\\\"architecture-review \\\",\\\"description\\\":\\\"Architecture pass\\\",\"}}]}}]}\n\n" +
			"data: {\"id\":\"chunk3\",\"object\":\"chat.completion.chunk\",\"model\":\"m1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"prompt\\\":\\\"Check worker boundaries\\\"}\"}}]}}]}\n\n" +
			"data: {\"id\":\"chunk4\",\"object\":\"chat.completion.chunk\",\"model\":\"m1\",\"choices\":[{\"index\":0,\"finish_reason\":\"tool_calls\",\"delta\":{}}]}\n\n" +
			"data: [DONE]\n\n",
	}
	var delegated []TaskDelegationRequest
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "upstream-model", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", ActorSubject: "dev@example.test", Agent: "governance-lead", Status: "running", SourceSystem: "opencode"}, nil
		},
		DelegateTask: func(_ context.Context, req TaskDelegationRequest) error {
			delegated = append(delegated, req)
			return nil
		},
	})

	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"delegate this"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_parent")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(delegated) != 1 {
		t.Fatalf("expected one task delegation, got %#v", delegated)
	}
	got := delegated[0]
	if got.ParentSessionID != "sess_parent" || got.ActorSubject != "dev@example.test" || got.ParentAgent != "governance-lead" {
		t.Fatalf("unexpected parent context: %#v", got)
	}
	if got.Agent != "architecture-review" || got.Description != "Architecture pass" || got.Prompt != "Check worker boundaries" {
		t.Fatalf("unexpected task delegation payload: %#v", got)
	}
	if got.ToolCallID != "id:call_task_1" || got.SourceSystem != "opencode-task" || got.ModelAlias != "coding-primary" {
		t.Fatalf("unexpected delegation metadata: %#v", got)
	}
}

func TestGatewayNonStreamCreatesTaskDelegationFromOpenCodeToolCall(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	backend := &rawFakeBackend{
		chat: []byte(`{"id":"chatcmpl_tool","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_task_1","type":"function","function":{"name":"task","arguments":"{\"agent\":\"security-review\",\"description\":\"Security pass\"}"}}]}}]}`),
	}
	var delegated []TaskDelegationRequest
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.NewWithRouteAvailability(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "upstream-model", AllowedClassifications: []string{"public", "internal"}},
		}}, nil),
		Backend: backend,
		Audit:   auditStore,
		NewID:   func(prefix string) string { return prefix + "_test" },
		LookupSession: func(context.Context, string) (SessionInfo, error) {
			return SessionInfo{Classification: "internal", ActorSubject: "dev@example.test", Agent: "governance-lead", Status: "running"}, nil
		},
		DelegateTask: func(_ context.Context, req TaskDelegationRequest) error {
			delegated = append(delegated, req)
			return nil
		},
	})

	body := []byte(`{"model":"coding-primary","messages":[{"role":"user","content":"delegate this"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_parent")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(delegated) != 1 || delegated[0].Agent != "security-review" || delegated[0].Description != "Security pass" {
		t.Fatalf("expected security-review delegation, got %#v", delegated)
	}
}
