package copilot

import (
	"context"
	"testing"
	"time"
)

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
