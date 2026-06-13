package governance

import (
	"context"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/oauth"
)

// OAuthTokenStoreAdapter adapts internal/oauth.TokenStore to the
// UserTokenStore interface expected by the MCP proxy.
type OAuthTokenStoreAdapter struct {
	store oauth.TokenStore
}

func NewOAuthTokenStoreAdapter(store oauth.TokenStore) *OAuthTokenStoreAdapter {
	return &OAuthTokenStoreAdapter{store: store}
}

func (a *OAuthTokenStoreAdapter) Token(ctx context.Context, userID string, serverID string) (string, bool) {
	if a == nil || a.store == nil {
		return "", false
	}
	tok, err := a.store.Get(ctx, userID, serverID)
	if err != nil {
		return "", false
	}
	if tok.IsExpired() {
		return "", false
	}
	return tok.AccessToken, true
}
