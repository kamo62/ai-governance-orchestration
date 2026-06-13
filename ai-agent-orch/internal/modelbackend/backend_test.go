package modelbackend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
)

func TestBifrostModelNamePrefixesProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{"openrouter", "openrouter", "deepseek/deepseek-v4-flash", "openrouter/deepseek/deepseek-v4-flash"},
		{"anthropic", "anthropic", "claude-haiku-4-5-20251001", "anthropic/claude-haiku-4-5-20251001"},
		{"bedrock", "bedrock", "anthropic.claude-3-5-sonnet-20240620-v1:0", "bedrock/anthropic.claude-3-5-sonnet-20240620-v1:0"},
		{"already prefixed", "openrouter", "openrouter/deepseek/deepseek-v4-flash", "openrouter/deepseek/deepseek-v4-flash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BifrostModelName(tt.provider, tt.model); got != tt.want {
				t.Fatalf("BifrostModelName(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}

func TestBifrostBackendPostsOpenAICompatibleChatCompletion(t *testing.T) {
	var gotAuth string
	var gotModel string
	var gotProviderHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("expected /v1/chat/completions, got %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotProviderHeader = r.Header.Get("X-AI-Orch-Provider")

		var body openrouter.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotModel = body.Model

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(openrouter.ChatCompletionResponse{
			ID:    "gen_bifrost",
			Model: body.Model,
			Choices: []struct {
				Message openrouter.Message `json:"message"`
			}{
				{Message: openrouter.Message{Role: "assistant", Content: "ok"}},
			},
			Usage: openrouter.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		})
	}))
	defer server.Close()

	backend := NewBifrostBackend(BifrostConfig{
		BaseURL:    server.URL,
		APIKey:     "runtime-token",
		HTTPClient: server.Client(),
	})

	resp, err := backend.ChatCompletion(context.Background(), openrouter.ChatCompletionRequest{
		Provider: "openrouter",
		Model:    "deepseek/deepseek-v4-flash",
		Messages: []openrouter.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	if gotAuth != "Bearer runtime-token" {
		t.Fatalf("unexpected auth header %q", gotAuth)
	}
	if gotProviderHeader != "openrouter" {
		t.Fatalf("unexpected provider header %q", gotProviderHeader)
	}
	if gotModel != "openrouter/deepseek/deepseek-v4-flash" {
		t.Fatalf("unexpected model %q", gotModel)
	}
	if resp.FirstContent() != "ok" {
		t.Fatalf("unexpected response content %q", resp.FirstContent())
	}
}

func TestBifrostBackendStreamUsesResolvedModel(t *testing.T) {
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body openrouter.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotModel = body.Model
		if !body.Stream {
			t.Fatal("expected stream=true")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	backend := NewBifrostBackend(BifrostConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	stream, err := backend.ChatCompletionStream(context.Background(), openrouter.ChatCompletionRequest{
		Provider: "bedrock",
		Model:    "anthropic.claude-3-5-sonnet-20240620-v1:0",
		Messages: []openrouter.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream returned error: %v", err)
	}
	defer stream.Close()
	body, _ := io.ReadAll(stream)
	if !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("unexpected stream body %q", string(body))
	}
	if gotModel != "bedrock/anthropic.claude-3-5-sonnet-20240620-v1:0" {
		t.Fatalf("unexpected model %q", gotModel)
	}
}

func TestBifrostBackendRawChatPreservesToolFields(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("expected /v1/chat/completions, got %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_raw","model":"bedrock/anthropic.claude","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{}"}}]}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	backend := NewBifrostBackend(BifrostConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	body := []byte(`{"model":"coding-bedrock","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"ok"}],"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}],"tool_choice":"auto"}`)
	resp, err := backend.ChatCompletionRaw(context.Background(), RawRequest{Provider: "bedrock", Model: "anthropic.claude", Body: body})
	if err != nil {
		t.Fatalf("ChatCompletionRaw returned error: %v", err)
	}
	if got["model"] != "bedrock/anthropic.claude" {
		t.Fatalf("expected resolved model, got %#v", got["model"])
	}
	if _, ok := got["tools"].([]any); !ok {
		t.Fatalf("expected tools to be preserved, got %#v", got)
	}
	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("expected messages to be preserved, got %#v", got["messages"])
	}
	if !strings.Contains(string(resp), "tool_calls") {
		t.Fatalf("expected raw response body, got %s", string(resp))
	}
}

func TestBifrostBackendRawResponsesUsesResponsesPath(t *testing.T) {
	var gotPath string
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_raw","model":"bedrock/anthropic.claude","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":3}}`))
	}))
	defer server.Close()

	backend := NewBifrostBackend(BifrostConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	body := []byte(`{"model":"coding-bedrock","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}`)
	resp, err := backend.ResponsesRaw(context.Background(), RawRequest{Provider: "bedrock", Model: "anthropic.claude", Body: body})
	if err != nil {
		t.Fatalf("ResponsesRaw returned error: %v", err)
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("expected /v1/responses, got %s", gotPath)
	}
	if got["model"] != "bedrock/anthropic.claude" {
		t.Fatalf("expected resolved model, got %#v", got["model"])
	}
	if _, ok := got["tools"].([]any); !ok {
		t.Fatalf("expected responses tools to be preserved, got %#v", got)
	}
	if !strings.Contains(string(resp), "input_tokens") {
		t.Fatalf("expected raw responses body, got %s", string(resp))
	}
}

func TestNewBackendRejectsUnknownBackend(t *testing.T) {
	_, err := New(BackendConfig{Name: "nope"})
	if err == nil {
		t.Fatal("expected unknown backend error")
	}
}

func TestNewBackendRejectsCopilotWithoutResolver(t *testing.T) {
	_, err := New(BackendConfig{Name: BackendCopilotUser})
	if err == nil {
		t.Fatal("expected missing copilot token resolver error")
	}
}

func TestRoutedBackendRoutesRawChatByProvider(t *testing.T) {
	defaultBackend := &recordRawBackend{name: "default"}
	copilotBackend := &recordRawBackend{name: BackendCopilotUser}
	routed := NewRoutedBackend(defaultBackend, map[string]Backend{BackendCopilotUser: copilotBackend})
	_, err := routed.ChatCompletionRaw(context.Background(), RawRequest{Provider: BackendCopilotUser, Model: "gpt-5-mini", Body: []byte(`{"model":"x"}`), ActorSubject: "dev"})
	if err != nil {
		t.Fatalf("ChatCompletionRaw returned error: %v", err)
	}
	if copilotBackend.calls != 1 || defaultBackend.calls != 0 {
		t.Fatalf("expected copilot route, default=%d copilot=%d", defaultBackend.calls, copilotBackend.calls)
	}
	if routed.ResolvedModel(BackendCopilotUser, "gpt-5-mini") != "copilot-user:gpt-5-mini" {
		t.Fatalf("expected routed resolved model")
	}
}

func TestRoutedBackendReportsProviderSupport(t *testing.T) {
	defaultBackend := NewCopilotUserBackend(nil, fakeCopilotResolver{})
	bifrostBackend := NewBifrostBackend(BifrostConfig{BaseURL: "http://bifrost.test"})
	routed := NewRoutedBackend(defaultBackend, map[string]Backend{"openrouter": bifrostBackend})
	if !routed.SupportsProvider("openrouter") {
		t.Fatal("expected explicit OpenRouter route to be supported")
	}
	if routed.SupportsProvider("anthropic") {
		t.Fatal("did not expect Copilot default backend to support unconfigured Anthropic provider")
	}
	if !BackendSupportsProvider(defaultBackend, BackendCopilotUser) || BackendSupportsProvider(defaultBackend, "openrouter") {
		t.Fatal("unexpected Copilot provider support")
	}
}

type recordRawBackend struct {
	name  string
	calls int
}

func (b *recordRawBackend) Name() string { return b.name }

func (b *recordRawBackend) ResolvedModel(_ string, model string) string { return b.name + ":" + model }

func (b *recordRawBackend) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, nil
}

func (b *recordRawBackend) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return nil, nil
}

func (b *recordRawBackend) ChatCompletionRaw(context.Context, RawRequest) ([]byte, error) {
	b.calls++
	return []byte(`{"ok":true}`), nil
}

func (b *recordRawBackend) ChatCompletionStreamRaw(context.Context, RawRequest) (io.ReadCloser, error) {
	b.calls++
	return io.NopCloser(strings.NewReader("data: [DONE]\n\n")), nil
}
