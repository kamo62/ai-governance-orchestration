package modelbackend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-agent-orch/internal/openrouter"
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

func TestNewBackendRejectsUnknownBackend(t *testing.T) {
	_, err := New(BackendConfig{Name: "nope"})
	if err == nil {
		t.Fatal("expected unknown backend error")
	}
}
