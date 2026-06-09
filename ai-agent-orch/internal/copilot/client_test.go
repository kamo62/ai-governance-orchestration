package copilot

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientModelsUsesCopilotHeaders(t *testing.T) {
	var gotAuth, gotIntent, gotInitiator string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIntent = r.Header.Get("Openai-Intent")
		gotInitiator = r.Header.Get("x-initiator")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	client := NewClient()
	client.CopilotBaseURL = server.URL
	client.HTTPClient = server.Client()
	if _, err := client.Models(context.Background(), "gho_token"); err != nil {
		t.Fatalf("Models: %v", err)
	}
	if gotAuth != "Bearer gho_token" || gotIntent != "conversation-edits" || gotInitiator != "user" {
		t.Fatalf("unexpected headers auth=%q intent=%q initiator=%q", gotAuth, gotIntent, gotInitiator)
	}
}

func TestClientChatCompletionSendsBody(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer server.Close()
	client := NewClient()
	client.CopilotBaseURL = server.URL
	client.HTTPClient = server.Client()
	if _, err := client.ChatCompletion(context.Background(), "gho_token", []byte(`{"model":"gpt-5-mini"}`)); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if !strings.Contains(gotBody, "gpt-5-mini") {
		t.Fatalf("unexpected body %q", gotBody)
	}
}
