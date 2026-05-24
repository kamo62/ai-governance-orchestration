package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatCompletionSendsOpenRouterRequest(t *testing.T) {
	var gotModel string
	var gotPrompt string
	var gotReasoning *ReasoningConfig

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("expected chat completions path, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("unexpected content type %q", got)
		}

		var body ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotModel = body.Model
		if len(body.Messages) != 1 {
			t.Fatalf("expected one message, got %d", len(body.Messages))
		}
		gotPrompt = body.Messages[0].Content
		gotReasoning = body.Reasoning

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "gen-test",
			"model": "test-model-id",
			"choices": [
				{"message": {"role": "assistant", "content": "smoke-ok"}}
			],
			"usage": {
				"prompt_tokens": 5,
				"completion_tokens": 2,
				"total_tokens": 7,
				"cost": 0.001,
				"completion_tokens_details": {"reasoning_tokens": 1}
			}
		}`))
	}))
	defer srv.Close()

	client := NewClient(Config{
		APIKey:     "test-key",
		BaseURL:    srv.URL + "/api/v1",
		HTTPClient: srv.Client(),
	})

	response, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model: "test-model-id",
		Messages: []Message{
			{Role: "user", Content: "Reply with smoke-ok."},
		},
		MaxTokens: 16,
		Reasoning: &ReasoningConfig{
			Effort:  "high",
			Exclude: true,
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletion returned error: %v", err)
	}
	if gotModel != "test-model-id" {
		t.Fatalf("unexpected model %q", gotModel)
	}
	if gotPrompt != "Reply with smoke-ok." {
		t.Fatalf("unexpected prompt %q", gotPrompt)
	}
	if gotReasoning == nil || gotReasoning.Effort != "high" || !gotReasoning.Exclude {
		t.Fatalf("unexpected reasoning config %#v", gotReasoning)
	}
	if response.FirstContent() != "smoke-ok" {
		t.Fatalf("unexpected response content %q", response.FirstContent())
	}
	if response.Usage.TotalTokens != 7 {
		t.Fatalf("unexpected total tokens %d", response.Usage.TotalTokens)
	}
	if response.Usage.CompletionTokensDetails.ReasoningTokens != 1 {
		t.Fatalf("unexpected reasoning tokens %d", response.Usage.CompletionTokensDetails.ReasoningTokens)
	}
}

func TestChatCompletionRequiresAPIKey(t *testing.T) {
	client := NewClient(Config{APIKey: ""})

	_, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model: "test-model-id",
		Messages: []Message{
			{Role: "user", Content: "Reply with smoke-ok."},
		},
	})
	if err == nil {
		t.Fatalf("expected missing API key error")
	}
}

func TestChatCompletionReportsOpenRouterErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exceeded", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := NewClient(Config{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	_, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model: "test-model-id",
		Messages: []Message{
			{Role: "user", Content: "Reply with smoke-ok."},
		},
	})
	if err == nil {
		t.Fatalf("expected OpenRouter error")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("unexpected error: %v", err)
	}
}
