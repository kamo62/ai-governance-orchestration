package modelbackend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/copilot"
)

type fakeCopilotResolver struct {
	rec copilot.TokenRecord
	err error
}

func (r fakeCopilotResolver) TokenForActor(context.Context, string) (copilot.TokenRecord, error) {
	return r.rec, r.err
}

func newCopilotTestServer(t *testing.T, exchangeStatus int, gotAuth *[]string, exchangeCalls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			*exchangeCalls++
			if exchangeStatus != http.StatusOK {
				w.WriteHeader(exchangeStatus)
				return
			}
			fmt.Fprintf(w, `{"token":"sess_bearer","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
		case "/chat/completions", "/responses":
			*gotAuth = append(*gotAuth, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"id":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newCopilotTestBackend(server *httptest.Server, resolver CopilotTokenResolver) *CopilotUserBackend {
	client := copilot.NewClient()
	client.GitHubAPIURL = server.URL
	client.CopilotBaseURL = server.URL
	client.HTTPClient = server.Client()
	client.StreamHTTPClient = server.Client()
	return NewCopilotUserBackend(client, resolver)
}

func TestCopilotBackendExchangesAndCachesSessionToken(t *testing.T) {
	var gotAuth []string
	exchangeCalls := 0
	server := newCopilotTestServer(t, http.StatusOK, &gotAuth, &exchangeCalls)
	defer server.Close()

	backend := newCopilotTestBackend(server, fakeCopilotResolver{rec: copilot.TokenRecord{ActorSubject: "dev", AccessToken: "gho_oauth", Fingerprint: "fp"}})
	req := RawRequest{Provider: BackendCopilotUser, Model: "gpt-5-mini", Body: []byte(`{"model":"x"}`), ActorSubject: "dev"}
	for i := 0; i < 2; i++ {
		if _, err := backend.ChatCompletionRaw(context.Background(), req); err != nil {
			t.Fatalf("ChatCompletionRaw: %v", err)
		}
	}
	if exchangeCalls != 1 {
		t.Fatalf("expected one cached exchange, got %d", exchangeCalls)
	}
	for _, auth := range gotAuth {
		if auth != "Bearer sess_bearer" {
			t.Fatalf("expected exchanged bearer, got %q", auth)
		}
	}
}

func TestCopilotBackendFallsBackToOAuthWhenExchangeFails(t *testing.T) {
	var gotAuth []string
	exchangeCalls := 0
	server := newCopilotTestServer(t, http.StatusNotFound, &gotAuth, &exchangeCalls)
	defer server.Close()

	backend := newCopilotTestBackend(server, fakeCopilotResolver{rec: copilot.TokenRecord{ActorSubject: "dev", AccessToken: "gho_oauth", Fingerprint: "fp"}})
	if _, err := backend.ChatCompletionRaw(context.Background(), RawRequest{Provider: BackendCopilotUser, Model: "gpt-5-mini", Body: []byte(`{"model":"x"}`), ActorSubject: "dev"}); err != nil {
		t.Fatalf("ChatCompletionRaw: %v", err)
	}
	if len(gotAuth) != 1 || gotAuth[0] != "Bearer gho_oauth" {
		t.Fatalf("expected OAuth fallback bearer, got %v", gotAuth)
	}
}

func TestCopilotBackendResponsesRaw(t *testing.T) {
	var gotAuth []string
	exchangeCalls := 0
	server := newCopilotTestServer(t, http.StatusOK, &gotAuth, &exchangeCalls)
	defer server.Close()

	backend := newCopilotTestBackend(server, fakeCopilotResolver{rec: copilot.TokenRecord{ActorSubject: "dev", AccessToken: "gho_oauth", Fingerprint: "fp"}})
	body, err := backend.ResponsesRaw(context.Background(), RawRequest{Provider: BackendCopilotUser, Model: "gpt-5.3-codex", Body: []byte(`{"model":"x","input":"hi"}`), ActorSubject: "dev"})
	if err != nil {
		t.Fatalf("ResponsesRaw: %v", err)
	}
	if !strings.Contains(string(body), "ok") {
		t.Fatalf("unexpected responses body %q", body)
	}
}

func TestCopilotBackendNotEnrolledErrorIsActionable(t *testing.T) {
	backend := NewCopilotUserBackend(copilot.NewClient(), fakeCopilotResolver{err: copilot.ErrTokenNotFound})
	_, err := backend.ChatCompletionRaw(context.Background(), RawRequest{Model: "gpt-5-mini", Body: []byte(`{"model":"x"}`), ActorSubject: "kamogelo"})
	if !errors.Is(err, copilot.ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "kamogelo") || !strings.Contains(err.Error(), "copilot login") {
		t.Fatalf("expected actionable enrollment error, got %q", err.Error())
	}
}
