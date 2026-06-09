package modelbackend

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"ai-agent-orch/internal/openrouter"
)

type RoutedBackend struct {
	defaultBackend Backend
	routes         map[string]Backend
}

func NewRoutedBackend(defaultBackend Backend, routes map[string]Backend) *RoutedBackend {
	clean := make(map[string]Backend, len(routes))
	for provider, backend := range routes {
		provider = strings.TrimSpace(provider)
		if provider == "" || backend == nil {
			continue
		}
		clean[provider] = backend
	}
	return &RoutedBackend{defaultBackend: defaultBackend, routes: clean}
}

func (b *RoutedBackend) Name() string {
	if b == nil || b.defaultBackend == nil {
		return ""
	}
	if len(b.routes) == 0 {
		return b.defaultBackend.Name()
	}
	names := make([]string, 0, len(b.routes))
	for _, route := range b.routes {
		names = append(names, route.Name())
	}
	sort.Strings(names)
	return b.defaultBackend.Name() + "+" + strings.Join(names, "+")
}

func (b *RoutedBackend) ResolvedModel(provider string, model string) string {
	return b.backendFor(provider).ResolvedModel(provider, model)
}

func (b *RoutedBackend) ChatCompletion(ctx context.Context, req openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return b.backendFor(req.Provider).ChatCompletion(ctx, req)
}

func (b *RoutedBackend) ChatCompletionStream(ctx context.Context, req openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return b.backendFor(req.Provider).ChatCompletionStream(ctx, req)
}

func (b *RoutedBackend) ChatCompletionRaw(ctx context.Context, req RawRequest) ([]byte, error) {
	backend := b.backendFor(req.Provider)
	raw, ok := backend.(RawChatBackend)
	if !ok {
		return nil, fmt.Errorf("backend %s does not support raw chat", backend.Name())
	}
	return raw.ChatCompletionRaw(ctx, req)
}

func (b *RoutedBackend) ChatCompletionStreamRaw(ctx context.Context, req RawRequest) (io.ReadCloser, error) {
	backend := b.backendFor(req.Provider)
	raw, ok := backend.(RawChatBackend)
	if !ok {
		return nil, fmt.Errorf("backend %s does not support raw chat streams", backend.Name())
	}
	return raw.ChatCompletionStreamRaw(ctx, req)
}

func (b *RoutedBackend) ResponsesRaw(ctx context.Context, req RawRequest) ([]byte, error) {
	backend := b.backendFor(req.Provider)
	raw, ok := backend.(RawResponsesBackend)
	if !ok {
		return nil, fmt.Errorf("backend %s does not support raw responses", backend.Name())
	}
	return raw.ResponsesRaw(ctx, req)
}

func (b *RoutedBackend) ResponsesStreamRaw(ctx context.Context, req RawRequest) (io.ReadCloser, error) {
	backend := b.backendFor(req.Provider)
	raw, ok := backend.(RawResponsesBackend)
	if !ok {
		return nil, fmt.Errorf("backend %s does not support raw response streams", backend.Name())
	}
	return raw.ResponsesStreamRaw(ctx, req)
}

func (b *RoutedBackend) backendFor(provider string) Backend {
	if b != nil {
		if route, ok := b.routes[strings.TrimSpace(provider)]; ok && route != nil {
			return route
		}
		if b.defaultBackend != nil {
			return b.defaultBackend
		}
	}
	return unavailableBackend{}
}

type unavailableBackend struct{}

func (unavailableBackend) Name() string { return "unavailable" }

func (unavailableBackend) ResolvedModel(_ string, model string) string { return model }

func (unavailableBackend) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return openrouter.ChatCompletionResponse{}, fmt.Errorf("model backend unavailable")
}

func (unavailableBackend) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return nil, fmt.Errorf("model backend unavailable")
}
