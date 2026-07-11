package copilot

import (
	"context"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
)

func TestStoreLegacyMigrationDoesNotHoldSchemaRowsDuringDDL(t *testing.T) {
	dsn := "file:copilot_legacy_migration?mode=memory&cache=shared"
	db, err := sqlitex.Open(dsn, "copilot migration test")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE copilot_user_tokens (actor_subject TEXT PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- (&Store{db: db, key: normalizeKey("test")}).migrate() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("migration timed out; possible open PRAGMA rows during DDL")
	}
}

func TestStoreSaveLoadDelete(t *testing.T) {
	store, err := OpenStore(t.TempDir()+"/tokens.db", "test-encryption-key")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	refreshExpires := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	if err := store.Save(ctx, TokenRecord{ActorSubject: "local-dev", GitHubLogin: "dev", GitHubUserID: "1", AccessToken: "gho_token", RefreshToken: "ghr_token", AccessExpiresAt: expires, RefreshExpiresAt: refreshExpires}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rec, err := store.Load(ctx, "local-dev")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.AccessToken != "gho_token" || rec.RefreshToken != "ghr_token" || rec.Fingerprint == "" || rec.BaseURL != DefaultCopilotBaseURL {
		t.Fatalf("unexpected record: %#v", rec)
	}
	if !rec.AccessExpiresAt.Equal(expires) || !rec.RefreshExpiresAt.Equal(refreshExpires) {
		t.Fatalf("unexpected expiry timestamps: %#v", rec)
	}
	updated, err := store.UpdateOAuthToken(ctx, "local-dev", AccessTokenResponse{AccessToken: "gho_new", RefreshToken: "ghr_new", ExpiresIn: 3600}, time.Now().UTC())
	if err != nil {
		t.Fatalf("UpdateOAuthToken: %v", err)
	}
	if updated.AccessToken != "gho_new" || updated.RefreshToken != "ghr_new" || updated.AccessExpiresAt.IsZero() {
		t.Fatalf("unexpected updated record: %#v", updated)
	}
	if err := store.Delete(ctx, "local-dev"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Load(ctx, "local-dev"); err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}
