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

// TestChatCompletionRejectsForeignProvider verifies the native client fails fast
// for a non-OpenRouter provider instead of silently dropping it and sending an
// unqualified model id to OpenRouter.
func TestChatCompletionRejectsForeignProvider(t *testing.T) {
	client := NewClient(Config{APIKey: "test-key", BaseURL: "http://127.0.0.1:0"})
	req := ChatCompletionRequest{
		Provider: "anthropic",
		Model:    "claude-haiku-4-5-20251001",
		Messages: []Message{{Role: "user", Content: "hi"}},
	}
	if _, err := client.ChatCompletion(context.Background(), req); err == nil {
		t.Fatal("expected error for non-openrouter provider in ChatCompletion")
	}
	if _, err := client.ChatCompletionStream(context.Background(), req); err == nil {
		t.Fatal("expected error for non-openrouter provider in ChatCompletionStream")
	}

	// The openrouter provider (and empty) must still be accepted past the guard.
	okReq := ChatCompletionRequest{Provider: "openrouter", Model: "x/y", Messages: req.Messages}
	if _, err := client.ChatCompletion(context.Background(), okReq); err != nil &&
		strings.Contains(err.Error(), "cannot route provider") {
		t.Fatalf("openrouter provider must pass the guard, got %v", err)
	}
}

func TestUsageUnmarshalAcceptsObjectCost(t *testing.T) {
	var usage Usage
	if err := json.Unmarshal([]byte(`{
		"prompt_tokens": 10,
		"completion_tokens": 5,
		"total_tokens": 15,
		"cost": {"prompt": 0.001, "completion": 0.002},
		"completion_tokens_details": {"reasoning_tokens": 3}
	}`), &usage); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}
	if usage.Cost < 0.0029 || usage.Cost > 0.0031 {
		t.Fatalf("expected summed cost 0.003, got %v", usage.Cost)
	}
	if usage.TotalTokens != 15 {
		t.Fatalf("expected total tokens 15, got %d", usage.TotalTokens)
	}
	if usage.CompletionTokensDetails.ReasoningTokens != 3 {
		t.Fatalf("expected reasoning tokens 3, got %d", usage.CompletionTokensDetails.ReasoningTokens)
	}
}

func TestUsageUnmarshalAcceptsStringCost(t *testing.T) {
	var usage Usage
	if err := json.Unmarshal([]byte(`{"total_tokens": 1, "cost": "0.004"}`), &usage); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}
	if usage.Cost != 0.004 {
		t.Fatalf("expected cost 0.004, got %v", usage.Cost)
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

func TestDecodeStreamChunkParsesUsageOnlyChunk(t *testing.T) {
	chunk, err := DecodeStreamChunk(`data: {"id":"chunk_usage","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16,"cost":"0.00002","completion_tokens_details":{"reasoning_tokens":1}}}`)
	if err != nil {
		t.Fatalf("DecodeStreamChunk returned error: %v", err)
	}
	if chunk.Usage == nil {
		t.Fatal("expected usage on stream chunk")
	}
	if chunk.Usage.PromptTokens != 12 || chunk.Usage.CompletionTokens != 4 || chunk.Usage.TotalTokens != 16 {
		t.Fatalf("unexpected usage: %#v", chunk.Usage)
	}
	if chunk.Usage.Cost != 0.00002 {
		t.Fatalf("expected parsed cost, got %v", chunk.Usage.Cost)
	}
	if chunk.Usage.CompletionTokensDetails.ReasoningTokens != 1 {
		t.Fatalf("expected reasoning tokens, got %d", chunk.Usage.CompletionTokensDetails.ReasoningTokens)
	}
}
