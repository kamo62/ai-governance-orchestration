package oauth

import (
	"context"
	"testing"
	"time"
)

func TestMemoryTokenStore(t *testing.T) {
	store := NewMemoryTokenStore()
	ctx := context.Background()

	// Set and get.
	tok := Token{
		AccessToken:  "abc123",
		RefreshToken: "refresh456",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour),
		Scopes:       []string{"read", "write"},
	}
	if err := store.Set(ctx, "user1", "github", tok); err != nil {
		t.Fatalf("set token: %v", err)
	}

	got, err := store.Get(ctx, "user1", "github")
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if got.AccessToken != "abc123" {
		t.Fatalf("expected access token abc123, got %q", got.AccessToken)
	}

	// Not found.
	if _, err := store.Get(ctx, "user1", "gitlab"); err == nil {
		t.Fatal("expected error for missing token")
	}

	// Delete.
	if err := store.Delete(ctx, "user1", "github"); err != nil {
		t.Fatalf("delete token: %v", err)
	}
	if _, err := store.Get(ctx, "user1", "github"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestTokenIsExpired(t *testing.T) {
	// Token expired 2 hours ago.
	tok := Token{ExpiresAt: time.Now().Add(-2 * time.Hour)}
	if !tok.IsExpired() {
		t.Fatal("expected expired token")
	}

	// Token expires in 2 hours.
	tok2 := Token{ExpiresAt: time.Now().Add(2 * time.Hour)}
	if tok2.IsExpired() {
		t.Fatal("expected non-expired token")
	}

	// Token expires in 30 seconds (within 60-second buffer).
	tok3 := Token{ExpiresAt: time.Now().Add(30 * time.Second)}
	if !tok3.IsExpired() {
		t.Fatal("expected token within buffer to be considered expired")
	}
}
