package openrouter

import (
	"context"
	"encoding/json"
	"io"
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

func TestChatCompletionStreamSendsStreamTrue(t *testing.T) {
	var gotStream bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("expected event-stream accept header, got %q", r.Header.Get("Accept"))
		}
		var body ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotStream = body.Stream

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	client := NewClient(Config{
		APIKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	reader, err := client.ChatCompletionStream(context.Background(), ChatCompletionRequest{
		Model:    "test-model-id",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletionStream returned error: %v", err)
	}
	defer reader.Close()
	_, _ = io.ReadAll(reader)

	if !gotStream {
		t.Fatal("expected stream=true in upstream request")
	}
}

func TestDecodeStreamChunkAcceptsDataPrefixWithoutSpace(t *testing.T) {
	chunk, err := DecodeStreamChunk(`data:{"id":"chunk1","choices":[{"index":0,"delta":{"content":"Hi"}}]}`)
	if err != nil {
		t.Fatalf("DecodeStreamChunk returned error: %v", err)
	}
	if chunk.ID != "chunk1" || chunk.Choices[0].Delta.Content != "Hi" {
		t.Fatalf("unexpected chunk: %#v", chunk)
	}

	_, err = DecodeStreamChunk("data:[DONE]")
	if err != io.EOF {
		t.Fatalf("expected EOF for DONE without space, got %v", err)
	}
}
