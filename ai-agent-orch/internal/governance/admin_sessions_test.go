package governance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func adminSessionsService(t *testing.T, sessions SessionStore) *SessionService {
	t.Helper()
	return NewSessionService(SessionConfig{
		DevToken:   "dev-token",
		AdminToken: "admin-token",
		Audit:      audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:   sessions,
	})
}

func TestAdminIdentityMapHandlerRequiresAdminToken(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	handler := NewAdminIdentityMapHandler(adminSessionsService(t, store))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/identity-map", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without admin token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminIdentityMapHandlerReturnsGroupedJSONArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	base := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	records := []SessionRecord{
		{
			SessionID: "sess_alice_1", ActorSubject: "dev@example.test", Agent: "managed-client",
			Classification: "internal", Status: "done", CreatedAt: base,
			ClaimedOSUsername: "alice", ClaimedHostname: "alice-old-host", ClaimedGithubLogin: "alice-gh",
		},
		{
			SessionID: "sess_alice_2", ActorSubject: "dev@example.test", Agent: "managed-client",
			Classification: "internal", Status: "done", CreatedAt: base.Add(time.Hour),
			ClaimedOSUsername: "alice", ClaimedHostname: "alice-new-host", ClaimedGithubLogin: "alice-gh",
		},
		{
			SessionID: "sess_no_identity", ActorSubject: "dev3@example.test", Agent: "managed-client",
			Classification: "internal", Status: "running", CreatedAt: base.Add(2 * time.Hour),
		},
	}
	for _, rec := range records {
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("create %s: %v", rec.SessionID, err)
		}
	}

	handler := NewAdminIdentityMapHandler(adminSessionsService(t, store))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/identity-map", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var entries []IdentityMapEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode response as JSON array: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one grouped row (no-identity session excluded), got %#v", entries)
	}
	entry := entries[0]
	if entry.ActorSubject != "dev@example.test" || entry.ClaimedOSUsername != "alice" || entry.ClaimedGithubLogin != "alice-gh" {
		t.Fatalf("unexpected identity map entry: %#v", entry)
	}
	if entry.ClaimedHostname != "alice-new-host" {
		t.Fatalf("expected hostname from the most recent session, got %q", entry.ClaimedHostname)
	}
	if !entry.LastSeen.Equal(base.Add(time.Hour)) {
		t.Fatalf("expected last_seen to be the max created_at, got %s", entry.LastSeen)
	}
}

func TestAdminSessionsExportCSVIncludesClaimedIdentityColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Create(ctx, SessionRecord{
		SessionID: "sess_export", ActorSubject: "dev@example.test", Agent: "managed-client",
		Classification: "internal", Status: "done", CreatedAt: time.Now().UTC(),
		ClaimedOSUsername: "alice", ClaimedHostname: "alice-laptop", ClaimedGithubLogin: "alice-gh",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	handler := NewAdminSessionsExportHandler(adminSessionsService(t, store))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/sessions/export", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, needle := range []string{"claimed_os_username", "claimed_github_login", "claimed_hostname", "alice", "alice-gh", "alice-laptop"} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected %q in CSV export, got:\n%s", needle, body)
		}
	}
}
