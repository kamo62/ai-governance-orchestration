package modelbackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"ai-agent-orch/internal/copilot"
	"ai-agent-orch/internal/openrouter"
)

const BackendCopilotUser = "copilot-user"

type CopilotTokenResolver interface {
	TokenForActor(context.Context, string) (copilot.TokenRecord, error)
}

type CopilotUserBackend struct {
	client   *copilot.Client
	resolver CopilotTokenResolver
}

func NewCopilotUserBackend(client *copilot.Client, resolver CopilotTokenResolver) *CopilotUserBackend {
	if client == nil {
		client = copilot.NewClient()
	}
	return &CopilotUserBackend{client: client, resolver: resolver}
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

func (b *CopilotUserBackend) prepare(ctx context.Context, req RawRequest) (string, []byte, error) {
	if b == nil || b.resolver == nil {
		return "", nil, errors.New("copilot token resolver unavailable")
	}
	if req.ActorSubject == "" {
		return "", nil, errors.New("actor subject is required for copilot-user")
	}
	rec, err := b.resolver.TokenForActor(ctx, req.ActorSubject)
	if err != nil {
		return "", nil, err
	}
	body, err := withRawModel(req.Body, req.Model)
	if err != nil {
		return "", nil, fmt.Errorf("prepare copilot request: %w", err)
	}
	return rec.AccessToken, body, nil
}

func (b *CopilotUserBackend) Models(ctx context.Context, actorSubject string) ([]byte, error) {
	if b == nil || b.resolver == nil {
		return nil, errors.New("copilot token resolver unavailable")
	}
	rec, err := b.resolver.TokenForActor(ctx, actorSubject)
	if err != nil {
		return nil, err
	}
	return b.client.Models(ctx, rec.AccessToken)
}

func normalizeCopilotResponseModel(body []byte, alias string) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	model, _ := json.Marshal(alias)
	obj["model"] = model
	encoded, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return encoded
}
