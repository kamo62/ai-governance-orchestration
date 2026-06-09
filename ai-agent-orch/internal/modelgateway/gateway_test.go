package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/modelbackend"
	"ai-agent-orch/internal/openrouter"
	"ai-agent-orch/internal/router"
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
		Router: router.New(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
				{Alias: "coding-fast", Provider: "openrouter", ModelID: "x-ai/grok-build-0.1", AllowedClassifications: []string{"public", "internal"}},
			},
		}),
		OpenRouter: &fakeChatClient{
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
		Router: router.New(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "upstream-model", AllowedClassifications: []string{"public", "internal"}},
		}}),
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
		Router: router.New(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "public-only", Provider: "openrouter", ModelID: "m-public", AllowedClassifications: []string{"public"}},
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "m-internal", AllowedClassifications: []string{"internal"}},
			},
		}),
		OpenRouter: &fakeChatClient{},
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
		Router: router.New(catalog.ModelRegistry{Models: []catalog.ModelDefinition{
			{Alias: "coding-primary", Provider: "openrouter", ModelID: "upstream-model", AllowedClassifications: []string{"public", "internal"}},
		}}),
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
		Router: router.New(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "m1", AllowedClassifications: []string{"public", "internal"}},
			},
		}),
		OpenRouter: streamClient,
		Audit:      auditStore,
		NewID:      func(prefix string) string { return prefix + "_test" },
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

func newTestGatewayWithBackend(backend *fakeChatClient, auditStore audit.Store) *Gateway {
	return NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.New(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "anthropic/claude-opus-4.7", AllowedClassifications: []string{"public", "internal"}},
				{Alias: "coding-fast", Provider: "openrouter", ModelID: "x-ai/grok-build-0.1", AllowedClassifications: []string{"public", "internal"}},
			},
		}),
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
