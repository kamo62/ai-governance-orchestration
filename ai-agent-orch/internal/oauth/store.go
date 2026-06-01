package oauth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TokenStore is the interface for user-scoped OAuth token storage.
// Implementations may be in-memory for local Phase 1 or backed by a
// secure token vault for team/organisation deployments.
type TokenStore interface {
	// Get returns the current access token for a user and provider.
	// If the token is expired and a refresh token is available,
	// implementations may automatically refresh.
	Get(ctx context.Context, userID, provider string) (Token, error)
	// Set stores or updates a token for a user and provider.
	Set(ctx context.Context, userID, provider string, token Token) error
	// Delete removes a token for a user and provider.
	Delete(ctx context.Context, userID, provider string) error
}

// Token represents an OAuth 2.0 access token with optional refresh.
type Token struct {
	AccessToken  string
	RefreshToken string
	TokenType    string // e.g., "Bearer"
	ExpiresAt    time.Time
	Scopes       []string
}

// IsExpired reports whether the token is expired with a 60-second clock skew buffer.
func (t Token) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().UTC().After(t.ExpiresAt.Add(-60 * time.Second))
}

// MemoryTokenStore is a local Phase 1 in-memory token store.
type MemoryTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]Token
}

func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{tokens: make(map[string]Token)}
}

func storeKey(userID, provider string) string {
	return fmt.Sprintf("%s::%s", userID, provider)
}

func (s *MemoryTokenStore) Get(ctx context.Context, userID, provider string) (Token, error) {
	if s == nil || s.tokens == nil {
		return Token{}, errors.New("token store not initialised")
	}
	if err := ctx.Err(); err != nil {
		return Token{}, err
	}
	key := storeKey(userID, provider)
	s.mu.RLock()
	defer s.mu.RUnlock()
	token, ok := s.tokens[key]
	if !ok {
		return Token{}, ErrTokenNotFound
	}
	return token, nil
}

func (s *MemoryTokenStore) Set(ctx context.Context, userID, provider string, token Token) error {
	if s == nil || s.tokens == nil {
		return errors.New("token store not initialised")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := storeKey(userID, provider)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[key] = token
	return nil
}

func (s *MemoryTokenStore) Delete(ctx context.Context, userID, provider string) error {
	if s == nil || s.tokens == nil {
		return errors.New("token store not initialised")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := storeKey(userID, provider)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, key)
	return nil
}

// TokenSource is an abstraction for acquiring OAuth tokens.
// Real implementations will perform browser-based or device-code flows.
type TokenSource interface {
	// Acquire initiates an OAuth flow and returns the resulting token.
	Acquire(ctx context.Context, userID, provider string, scopes []string) (Token, error)
}

// ErrTokenNotFound is returned when a token is not found in the store.
var ErrTokenNotFound = errors.New("token not found")
