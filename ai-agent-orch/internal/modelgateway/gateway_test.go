package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/openrouter"
	"ai-agent-orch/internal/router"
)

type fakeChatClient struct {
	resp openrouter.ChatCompletionResponse
	err  error
}

func (f *fakeChatClient) ChatCompletion(_ context.Context, _ openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return f.resp, f.err
}

func (f *fakeChatClient) ChatCompletionStream(_ context.Context, _ openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
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
	g := newTestGateway()
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

func TestGatewayResponsesSuccess(t *testing.T) {
	g := newTestGateway()
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
	g := NewGateway(GatewayConfig{
		RuntimeToken: "runtime-test-token",
		Router: router.New(catalog.ModelRegistry{
			Models: []catalog.ModelDefinition{
				{Alias: "coding-primary", Provider: "openrouter", ModelID: "m1", AllowedClassifications: []string{"public", "internal"}},
			},
		}),
		OpenRouter: &streamFakeClient{},
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
}

type streamFakeClient struct{}

func (s *streamFakeClient) ChatCompletion(_ context.Context, _ openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, nil
}

func (s *streamFakeClient) ChatCompletionStream(_ context.Context, _ openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	data := `data: {"id":"chunk1","object":"chat.completion.chunk","model":"m1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	return io.NopCloser(strings.NewReader(data)), nil
}
