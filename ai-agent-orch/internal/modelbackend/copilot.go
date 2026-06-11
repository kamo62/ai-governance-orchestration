package modelbackend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"ai-agent-orch/internal/copilot"
	"ai-agent-orch/internal/openrouter"
)

const BackendCopilotUser = "copilot-user"

// sessionTokenExpiryMargin forces a re-exchange shortly before the short-lived
// Copilot bearer expires so in-flight requests never carry a stale token.
const sessionTokenExpiryMargin = 2 * time.Minute

type CopilotTokenResolver interface {
	TokenForActor(context.Context, string) (copilot.TokenRecord, error)
}

type CopilotUserBackend struct {
	client   *copilot.Client
	resolver CopilotTokenResolver

	mu            sync.Mutex
	sessionTokens map[string]copilot.SessionToken
}

func NewCopilotUserBackend(client *copilot.Client, resolver CopilotTokenResolver) *CopilotUserBackend {
	if client == nil {
		client = copilot.NewClient()
	}
	return &CopilotUserBackend{
		client:        client,
		resolver:      resolver,
		sessionTokens: make(map[string]copilot.SessionToken),
	}
}

func (b *CopilotUserBackend) Name() string { return BackendCopilotUser }

func (b *CopilotUserBackend) ResolvedModel(_ string, model string) string { return model }

func (b *CopilotUserBackend) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, errors.New("copilot-user requires raw actor-bound requests")
}

func (b *CopilotUserBackend) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return nil, errors.New("copilot-user requires raw actor-bound requests")
}

func (b *CopilotUserBackend) ChatCompletionRaw(ctx context.Context, req RawRequest) ([]byte, error) {
	token, body, err := b.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	return b.client.ChatCompletion(ctx, token, body)
}

func (b *CopilotUserBackend) ChatCompletionStreamRaw(ctx context.Context, req RawRequest) (io.ReadCloser, error) {
	token, body, err := b.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	return b.client.ChatCompletionStream(ctx, token, body)
}

func (b *CopilotUserBackend) ResponsesRaw(ctx context.Context, req RawRequest) ([]byte, error) {
	token, body, err := b.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	return b.client.Responses(ctx, token, body)
}

func (b *CopilotUserBackend) ResponsesStreamRaw(ctx context.Context, req RawRequest) (io.ReadCloser, error) {
	token, body, err := b.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	return b.client.ResponsesStream(ctx, token, body)
}

func (b *CopilotUserBackend) prepare(ctx context.Context, req RawRequest) (string, []byte, error) {
	if b == nil || b.resolver == nil {
		return "", nil, errors.New("copilot token resolver unavailable")
	}
	if req.ActorSubject == "" {
		return "", nil, errors.New("actor subject is required for copilot-user")
	}
	rec, err := b.resolveToken(ctx, req.ActorSubject)
	if err != nil {
		return "", nil, err
	}
	body, err := withRawModel(req.Body, req.Model)
	if err != nil {
		return "", nil, fmt.Errorf("prepare copilot request: %w", err)
	}
	return b.bearerForRecord(ctx, rec), body, nil
}

func (b *CopilotUserBackend) Models(ctx context.Context, actorSubject string) ([]byte, error) {
	if b == nil || b.resolver == nil {
		return nil, errors.New("copilot token resolver unavailable")
	}
	rec, err := b.resolveToken(ctx, actorSubject)
	if err != nil {
		return nil, err
	}
	return b.client.Models(ctx, b.bearerForRecord(ctx, rec))
}

func (b *CopilotUserBackend) resolveToken(ctx context.Context, actorSubject string) (copilot.TokenRecord, error) {
	rec, err := b.resolver.TokenForActor(ctx, actorSubject)
	if errors.Is(err, copilot.ErrTokenNotFound) {
		return copilot.TokenRecord{}, fmt.Errorf("%w for actor %q; enroll with `ai-orch copilot login`", err, actorSubject)
	}
	if errors.Is(err, copilot.ErrTokenRevoked) {
		return copilot.TokenRecord{}, fmt.Errorf("%w for actor %q; re-enroll with `ai-orch copilot login`", err, actorSubject)
	}
	return rec, err
}

// bearerForRecord exchanges the stored GitHub OAuth token for the short-lived
// Copilot API bearer, caching it per actor until it nears expiry. If the
// exchange endpoint rejects the request, the OAuth token is used directly so
// accounts where direct OAuth still works keep functioning.
func (b *CopilotUserBackend) bearerForRecord(ctx context.Context, rec copilot.TokenRecord) string {
	cacheKey := rec.ActorSubject + "\x00" + rec.Fingerprint
	b.mu.Lock()
	cached, ok := b.sessionTokens[cacheKey]
	b.mu.Unlock()
	if ok && !cached.Expired(sessionTokenExpiryMargin) {
		return cached.Token
	}
	exchanged, err := b.client.ExchangeSessionToken(ctx, rec.AccessToken)
	if err != nil {
		log.Printf("copilot session token exchange failed for actor %s; using OAuth token directly: %v", rec.ActorSubject, err)
		return rec.AccessToken
	}
	b.mu.Lock()
	b.sessionTokens[cacheKey] = exchanged
	b.mu.Unlock()
	return exchanged.Token
}
