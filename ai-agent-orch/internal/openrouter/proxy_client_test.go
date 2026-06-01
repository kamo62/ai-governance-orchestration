package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyClient_ChatCompletion(t *testing.T) {
	var gotSessionID string
	var gotAlias string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat" {
			t.Fatalf("expected /chat, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer svc-token" {
			t.Fatalf("unexpected auth: %s", auth)
		}
		gotSessionID = r.Header.Get("X-AI-Orch-Session-ID")
		gotAlias = r.Header.Get("X-AI-Orch-Model-Alias")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:    "proxy-gen",
			Model: "test-model",
			Choices: []struct {
				Message Message `json:"message"`
			}{
				{Message: Message{Role: "assistant", Content: "proxy-ok"}},
			},
		})
	}))
	defer srv.Close()

	client := NewProxyClient(ProxyConfig{
		BaseURL:      srv.URL,
		ServiceToken: "svc-token",
		HTTPClient:   srv.Client(),
	})

	resp, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		SessionID:  "sess_proxy",
		ModelAlias: "coding-balanced",
		Model:      "test-model",
		Messages:   []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FirstContent() != "proxy-ok" {
		t.Fatalf("unexpected content: %s", resp.FirstContent())
	}
	if gotSessionID != "sess_proxy" {
		t.Fatalf("unexpected session id: %s", gotSessionID)
	}
	if gotAlias != "coding-balanced" {
		t.Fatalf("unexpected model alias: %s", gotAlias)
	}
}

func TestProxyClient_Validation(t *testing.T) {
	client := NewProxyClient(ProxyConfig{BaseURL: "", ServiceToken: "token"})
	_, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error for missing baseURL")
	}

	client = NewProxyClient(ProxyConfig{BaseURL: "http://x", ServiceToken: ""})
	_, err = client.ChatCompletion(context.Background(), ChatCompletionRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error for missing service token")
	}

	client = NewProxyClient(ProxyConfig{BaseURL: "http://x", ServiceToken: "token"})
	_, err = client.ChatCompletion(context.Background(), ChatCompletionRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error for missing session ID")
	}
}

func TestChatCompletionResponse_FirstContent(t *testing.T) {
	r := ChatCompletionResponse{
		Choices: []struct {
			Message Message `json:"message"`
		}{
			{Message: Message{Role: "assistant", Content: "hello"}},
		},
	}
	if r.FirstContent() != "hello" {
		t.Fatalf("unexpected first content: %s", r.FirstContent())
	}

	empty := ChatCompletionResponse{}
	if empty.FirstContent() != "" {
		t.Fatalf("expected empty content for no choices")
	}
}
