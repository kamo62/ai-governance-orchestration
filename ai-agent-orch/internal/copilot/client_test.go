package copilot

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientModelsUsesCopilotHeaders(t *testing.T) {
	var gotAuth, gotIntent, gotInitiator, gotEditor, gotPlugin string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIntent = r.Header.Get("Openai-Intent")
		gotInitiator = r.Header.Get("x-initiator")
		gotEditor = r.Header.Get("Editor-Version")
		gotPlugin = r.Header.Get("Editor-Plugin-Version")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	client := NewClient()
	client.CopilotBaseURL = server.URL
	client.HTTPClient = server.Client()
	if _, err := client.Models(context.Background(), "gho_token"); err != nil {
		t.Fatalf("Models: %v", err)
	}
	if gotAuth != "Bearer gho_token" || gotIntent != "conversation-edits" || gotInitiator != "user" || gotEditor == "" || gotPlugin == "" {
		t.Fatalf("unexpected headers auth=%q intent=%q initiator=%q editor=%q plugin=%q", gotAuth, gotIntent, gotInitiator, gotEditor, gotPlugin)
	}
}

func TestClientRefreshAccessToken(t *testing.T) {
	var gotGrant, gotRefresh string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotGrant = r.Form.Get("grant_type")
		gotRefresh = r.Form.Get("refresh_token")
		_, _ = w.Write([]byte(`{"access_token":"gho_new","refresh_token":"ghr_new","expires_in":3600,"refresh_token_expires_in":7200}`))
	}))
	defer server.Close()
	client := NewClient()
	client.GitHubBaseURL = server.URL
	client.HTTPClient = server.Client()
	token, err := client.RefreshAccessToken(context.Background(), "ghr_old")
	if err != nil {
		t.Fatalf("RefreshAccessToken: %v", err)
	}
	if gotGrant != "refresh_token" || gotRefresh != "ghr_old" {
		t.Fatalf("unexpected form grant=%q refresh=%q", gotGrant, gotRefresh)
	}
	if token.AccessToken != "gho_new" || token.RefreshToken != "ghr_new" {
		t.Fatalf("unexpected token: %#v", token)
	}
	if token.AccessExpiresAt(time.Now()).IsZero() || token.RefreshExpiresAt(time.Now()).IsZero() {
		t.Fatal("expected expiry helpers to return timestamps")
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
