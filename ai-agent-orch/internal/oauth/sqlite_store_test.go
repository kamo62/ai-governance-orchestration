package oauth

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteTokenStoreRoundTripAndReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	ctx := context.Background()

	store, err := NewSQLiteTokenStore(dbPath, "test-encryption-key")
	if err != nil {
		t.Fatalf("NewSQLiteTokenStore: %v", err)
	}
	tok := Token{
		AccessToken:  "abc123",
		RefreshToken: "refresh456",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour).UTC(),
		Scopes:       []string{"read"},
	}
	if err := store.Set(ctx, "user1", "github", tok); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewSQLiteTokenStore(dbPath, "test-encryption-key")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.Get(ctx, "user1", "github")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.AccessToken != "abc123" || got.RefreshToken != "refresh456" {
		t.Fatalf("unexpected token after reopen: %+v", got)
	}
	if err := reopened.Delete(ctx, "user1", "github"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := reopened.Get(ctx, "user1", "github"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestSQLiteTokenStoreWrongKeyFailsClosed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	ctx := context.Background()

	store, err := NewSQLiteTokenStore(dbPath, "key-one")
	if err != nil {
		t.Fatalf("NewSQLiteTokenStore: %v", err)
	}
	if err := store.Set(ctx, "user1", "github", Token{AccessToken: "secret"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	store.Close()

	wrongKey, err := NewSQLiteTokenStore(dbPath, "key-two")
	if err != nil {
		t.Fatalf("reopen with wrong key: %v", err)
	}
	defer wrongKey.Close()
	if _, err := wrongKey.Get(ctx, "user1", "github"); err == nil {
		t.Fatal("expected decryption failure with wrong key")
	}
}
