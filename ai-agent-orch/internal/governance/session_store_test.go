package governance

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSQLiteSessionStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sessions.db"

	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	rec := SessionRecord{
		SessionID:      "sess_test",
		ActorSubject:   "user-1",
		Agent:          "test-generation",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "created",
		CreatedAt:      time.Now().UTC(),
	}

	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.Get(ctx, "sess_test")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ActorSubject != "user-1" {
		t.Fatalf("expected actor user-1, got %s", got.ActorSubject)
	}
	if got.Status != "created" {
		t.Fatalf("expected status created, got %s", got.Status)
	}

	if err := store.CompareAndSwapStatus(ctx, "sess_test", "created", "confirmed"); err != nil {
		t.Fatalf("cas: %v", err)
	}

	got2, err := store.Get(ctx, "sess_test")
	if err != nil {
		t.Fatalf("get after cas: %v", err)
	}
	if got2.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %s", got2.Status)
	}

	if err := store.CompareAndSwapStatus(ctx, "sess_test", "created", "running"); err == nil {
		t.Fatal("expected cas to fail with wrong from status")
	}
}

func TestSQLiteSessionStoreNotFound(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sessions.db"

	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	_, err = store.Get(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected not found")
	}
}

func TestSQLiteSessionStoreReadsLegacyRowsAfterMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			actor_subject TEXT NOT NULL,
			agent TEXT NOT NULL,
			classification TEXT NOT NULL,
			prompt_sha256 TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		INSERT INTO sessions (
			session_id, actor_subject, agent, classification, prompt_sha256, status, created_at
		) VALUES (
			'sess_legacy', 'local-dev', 'test-generation', 'internal', 'abc123', 'created', ?
		);
	`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		_ = db.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	got, err := store.Get(context.Background(), "sess_legacy")
	if err != nil {
		t.Fatalf("get legacy session: %v", err)
	}
	if got.SessionID != "sess_legacy" {
		t.Fatalf("unexpected session id: %s", got.SessionID)
	}
	if got.UseCaseID != "" || got.StoryPoints != 0 || got.BaselineCostUSD != 0 {
		t.Fatalf("expected zero-value migrated fields, got %#v", got)
	}
}

func TestSQLiteSessionStoreCreatesDirectoryAndRestrictsPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "sessions.db")

	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat session db: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected session db permissions 0600, got %o", got)
	}
}

func TestSQLiteSessionStoreRequiresPath(t *testing.T) {
	if _, err := NewSQLiteSessionStore(""); err == nil {
		t.Fatal("expected empty path to fail")
	}
}
