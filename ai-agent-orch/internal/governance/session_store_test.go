package governance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
