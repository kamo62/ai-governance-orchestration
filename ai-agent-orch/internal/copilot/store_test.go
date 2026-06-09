package copilot

import (
	"context"
	"testing"
)

func TestStoreSaveLoadDelete(t *testing.T) {
	store, err := OpenStore(t.TempDir()+"/tokens.db", "test-encryption-key")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Save(ctx, TokenRecord{ActorSubject: "local-dev", GitHubLogin: "dev", GitHubUserID: "1", AccessToken: "gho_token"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rec, err := store.Load(ctx, "local-dev")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.AccessToken != "gho_token" || rec.Fingerprint == "" || rec.BaseURL != DefaultCopilotBaseURL {
		t.Fatalf("unexpected record: %#v", rec)
	}
	if err := store.Delete(ctx, "local-dev"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Load(ctx, "local-dev"); err != ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
}
