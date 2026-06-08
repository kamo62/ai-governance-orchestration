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
	body := []byte(`{"model":"coding-primary","input":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer runtime-test-token")
	req.Header.Set("X-AI-Orch-Session-ID", "sess_model_gateway")
	rec := httptest.NewRecorder()
	g.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result openAIResponsesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Model != "coding-primary" {
		t.Fatalf("expected alias in response model field, got %q", result.Model)
	}
	if len(result.Output) != 1 {
		t.Fatalf("expected 1 output, got %d", len(result.Output))
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
	if !strings.Contains(rec.Body.String(), `"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16}`) {
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
