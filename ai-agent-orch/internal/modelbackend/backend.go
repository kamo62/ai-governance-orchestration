// Package modelbackend adapts governed model requests to provider gateway backends.
package modelbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-agent-orch/internal/openrouter"
)

const (
	BackendNativeOpenRouter = "native-openrouter"
	BackendBifrost          = "bifrost"
)

type Backend interface {
	Name() string
	ResolvedModel(provider string, model string) string
	ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error)
	ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error)
}

type BackendConfig struct {
	Name             string
	OpenRouterClient openrouter.ChatClient
	BifrostBaseURL   string
	BifrostAPIKey    string
	HTTPClient       *http.Client
}

func New(cfg BackendConfig) (Backend, error) {
	switch strings.TrimSpace(cfg.Name) {
	case "", BackendNativeOpenRouter:
		if cfg.OpenRouterClient == nil {
			return nil, errors.New("native OpenRouter backend requires an OpenRouter client")
		}
		return NewOpenRouterBackend(cfg.OpenRouterClient), nil
	case BackendBifrost:
		if strings.TrimSpace(cfg.BifrostBaseURL) == "" {
			return nil, errors.New("Bifrost backend requires a base URL")
		}
		return NewBifrostBackend(BifrostConfig{
			BaseURL:    cfg.BifrostBaseURL,
			APIKey:     cfg.BifrostAPIKey,
			HTTPClient: cfg.HTTPClient,
		}), nil
	default:
		return nil, fmt.Errorf("unknown model backend %q", cfg.Name)
	}
}

type OpenRouterBackend struct {
	client openrouter.ChatClient
}

func NewOpenRouterBackend(client openrouter.ChatClient) *OpenRouterBackend {
	return &OpenRouterBackend{client: client}
}

func (b *OpenRouterBackend) Name() string {
	return BackendNativeOpenRouter
}

func (b *OpenRouterBackend) ResolvedModel(_ string, model string) string {
	return model
}

func (b *OpenRouterBackend) ChatCompletion(ctx context.Context, req openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	if req.Provider != "" && req.Provider != "openrouter" {
		return openrouter.ChatCompletionResponse{}, fmt.Errorf("native OpenRouter backend cannot handle provider %q", req.Provider)
	}
	if b == nil || b.client == nil {
		return openrouter.ChatCompletionResponse{}, errors.New("native OpenRouter backend unavailable")
	}
	return b.client.ChatCompletion(ctx, req)
}

func (b *OpenRouterBackend) ChatCompletionStream(ctx context.Context, req openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	if req.Provider != "" && req.Provider != "openrouter" {
		return nil, fmt.Errorf("native OpenRouter backend cannot handle provider %q", req.Provider)
	}
	if b == nil || b.client == nil {
		return nil, errors.New("native OpenRouter backend unavailable")
	}
	return b.client.ChatCompletionStream(ctx, req)
}

type BifrostConfig struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type BifrostBackend struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	streamHTTP *http.Client
}

func NewBifrostBackend(cfg BifrostConfig) *BifrostBackend {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	streamHTTP := cfg.HTTPClient
	if streamHTTP == nil {
		streamHTTP = &http.Client{Timeout: 0}
	}
	return &BifrostBackend{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:     cfg.APIKey,
		httpClient: httpClient,
		streamHTTP: streamHTTP,
	}
}

func (b *BifrostBackend) Name() string {
	return BackendBifrost
}

func (b *BifrostBackend) ResolvedModel(provider string, model string) string {
	return BifrostModelName(provider, model)
}

func BifrostModelName(provider string, model string) string {
	provider = strings.Trim(strings.ToLower(provider), "/ ")
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return model
	}
	if strings.HasPrefix(model, provider+"/") {
		return model
	}
	return provider + "/" + model
}

func (b *BifrostBackend) ChatCompletion(ctx context.Context, req openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	if b == nil || b.baseURL == "" {
		return openrouter.ChatCompletionResponse{}, errors.New("Bifrost backend base URL is required")
	}
	if req.Model == "" {
		return openrouter.ChatCompletionResponse{}, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return openrouter.ChatCompletionResponse{}, errors.New("at least one message is required")
	}
	body, err := b.encodeRequest(req)
	if err != nil {
		return openrouter.ChatCompletionResponse{}, err
	}
	httpReq, err := b.newRequest(ctx, http.MethodPost, b.baseURL+"/v1/chat/completions", bytes.NewReader(body), req.Provider)
	if err != nil {
		return openrouter.ChatCompletionResponse{}, err
	}
	resp, err := b.httpClient.Do(httpReq)
	if err != nil {
		return openrouter.ChatCompletionResponse{}, fmt.Errorf("call Bifrost chat completions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return openrouter.ChatCompletionResponse{}, fmt.Errorf("Bifrost returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result openrouter.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return openrouter.ChatCompletionResponse{}, fmt.Errorf("decode Bifrost response: %w", err)
	}
	return result, nil
}

func (b *BifrostBackend) ChatCompletionStream(ctx context.Context, req openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	if b == nil || b.baseURL == "" {
		return nil, errors.New("Bifrost backend base URL is required")
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("at least one message is required")
	}
	req.Stream = true
	body, err := b.encodeRequest(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := b.newRequest(ctx, http.MethodPost, b.baseURL+"/v1/chat/completions", bytes.NewReader(body), req.Provider)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := b.streamHTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call Bifrost chat completions stream: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("Bifrost returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.Body, nil
}

func (b *BifrostBackend) Health(ctx context.Context) error {
	if b == nil || b.baseURL == "" {
		return errors.New("Bifrost backend base URL is required")
	}
	req, err := b.newRequest(ctx, http.MethodGet, b.baseURL+"/health", nil, "")
	if err != nil {
		return err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Bifrost health check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Bifrost health returned %d", resp.StatusCode)
	}
	return nil
}

func (b *BifrostBackend) encodeRequest(req openrouter.ChatCompletionRequest) ([]byte, error) {
	req.Model = b.ResolvedModel(req.Provider, req.Model)
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode Bifrost request: %w", err)
	}
	return body, nil
}

func (b *BifrostBackend) newRequest(ctx context.Context, method string, url string, body io.Reader, provider string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create Bifrost request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	if provider != "" {
		req.Header.Set("X-AI-Orch-Provider", provider)
	}
	return req, nil
}
