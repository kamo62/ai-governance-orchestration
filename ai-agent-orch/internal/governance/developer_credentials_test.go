package governance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/copilot"
)

func TestSQLiteDeveloperCredentialStoreIssuesValidNinetyDayActorToken(t *testing.T) {
	store, err := NewSQLiteDeveloperCredentialStore(":memory:")
	if err != nil {
		t.Fatalf("new credential store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	record, token, err := store.Issue(context.Background(), DeveloperCredentialIssue{
		ActorSubject: "dev@example.test",
		Client:       "opencode",
		DeviceName:   "Kamo-MacBook-Pro",
		Now:          now,
		TTL:          90 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}
	if token == "" || !strings.HasPrefix(token, "air_") {
		t.Fatalf("expected air_ runtime token, got %q", token)
	}
	if record.ActorSubject != "dev@example.test" || record.Client != "opencode" {
		t.Fatalf("unexpected credential record: %#v", record)
	}
	if record.DeviceNameHash == "" || strings.Contains(record.DeviceNameHash, "MacBook") {
		t.Fatalf("expected hashed device name, got %q", record.DeviceNameHash)
	}
	if got := record.ExpiresAt.Sub(record.IssuedAt); got != 90*24*time.Hour {
		t.Fatalf("expected 90 day ttl, got %s", got)
	}

	validated, ok, err := store.Validate(context.Background(), token, now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("validate credential: %v", err)
	}
	if !ok || validated.ActorSubject != "dev@example.test" || validated.Client != "opencode" {
		t.Fatalf("expected valid actor-bound credential, ok=%t record=%#v", ok, validated)
	}

	if _, ok, err := store.Validate(context.Background(), token, now.Add(91*24*time.Hour)); err != nil || ok {
		t.Fatalf("expected expired credential to fail, ok=%t err=%v", ok, err)
	}
}

func TestDeveloperHandlerIssuesRuntimeCredentialOnlyAfterCopilotEnrollment(t *testing.T) {
	credStore, err := NewSQLiteDeveloperCredentialStore(":memory:")
	if err != nil {
		t.Fatalf("new credential store: %v", err)
	}
	defer credStore.Close()

	copilotStore, err := copilot.OpenStore(":memory:", "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("new copilot store: %v", err)
	}
	defer copilotStore.Close()

	if err := copilotStore.Save(context.Background(), copilot.TokenRecord{
		ActorSubject: "dev@example.test",
		GitHubLogin:  "dev",
		GitHubUserID: "42",
		BaseURL:      copilot.DefaultCopilotBaseURL,
		AccessToken:  "gho_dev",
	}); err != nil {
		t.Fatalf("save copilot token: %v", err)
	}

	handler := NewDeveloperHandler(DeveloperHandlerConfig{
		DevToken:             "dev-token",
		CopilotStore:         copilotStore,
		CredentialStore:      credStore,
		Now:                  func() time.Time { return time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC) },
		RuntimeCredentialTTL: 90 * 24 * time.Hour,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/developer/runtime-credential", strings.NewReader(`{"client":"opencode","device_name":"Kamo-MacBook-Pro"}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-AI-Orch-Local-Identity", "dev@example.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "gho_dev") || strings.Contains(rec.Body.String(), "Kamo-MacBook-Pro") {
		t.Fatalf("response leaked provider token or raw device name: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"runtime_token":"air_`) {
		t.Fatalf("expected runtime token response, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"expires_in_days":90`) {
		t.Fatalf("expected 90 day credential response, got %s", rec.Body.String())
	}
}

func TestDeveloperHandlerRejectsRuntimeCredentialWithoutCopilotEnrollment(t *testing.T) {
	credStore, err := NewSQLiteDeveloperCredentialStore(":memory:")
	if err != nil {
		t.Fatalf("new credential store: %v", err)
	}
	defer credStore.Close()
	copilotStore, err := copilot.OpenStore(":memory:", "0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("new copilot store: %v", err)
	}
	defer copilotStore.Close()

	handler := NewDeveloperHandler(DeveloperHandlerConfig{DevToken: "dev-token", CopilotStore: copilotStore, CredentialStore: credStore})
	req := httptest.NewRequest(http.MethodPost, "/v1/developer/runtime-credential", strings.NewReader(`{"client":"opencode"}`))
	req.Header.Set("Authorization", "Bearer dev-token")
	req.Header.Set("X-AI-Orch-Local-Identity", "dev@example.test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected missing Copilot enrollment to fail with 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
