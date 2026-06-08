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
	BackendAgentGateway     = "agentgateway"
)

type Backend interface {
	Name() string
	ResolvedModel(provider string, model string) string
	ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error)
	ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error)
}

type BackendConfig struct {
	Name                     string
	OpenRouterClient         openrouter.ChatClient
	BifrostBaseURL           string
	BifrostAPIKey            string
	AgentGatewayBaseURL      string
	AgentGatewayAPIKey       string
	AgentGatewayReadinessURL string
	HTTPClient               *http.Client
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
	case BackendAgentGateway:
		if strings.TrimSpace(cfg.AgentGatewayBaseURL) == "" {
			return nil, errors.New("agentgateway backend requires a base URL")
		}
		return NewAgentGatewayBackend(AgentGatewayConfig{
			BaseURL:      cfg.AgentGatewayBaseURL,
			APIKey:       cfg.AgentGatewayAPIKey,
			ReadinessURL: cfg.AgentGatewayReadinessURL,
			HTTPClient:   cfg.HTTPClient,
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
	*openAICompatibleBackend
}

func NewBifrostBackend(cfg BifrostConfig) *BifrostBackend {
	return &BifrostBackend{
		openAICompatibleBackend: newOpenAICompatibleBackend(openAICompatibleConfig{
			Name:           BackendBifrost,
			DisplayName:    "Bifrost",
			BaseURL:        cfg.BaseURL,
			APIKey:         cfg.APIKey,
			HealthURL:      strings.TrimRight(cfg.BaseURL, "/") + "/health",
			ProviderHeader: "X-AI-Orch-Provider",
			ResolveModel:   BifrostModelName,
			HTTPClient:     cfg.HTTPClient,
		}),
	}
}

func BifrostModelName(provider string, model string) string {
	provider = strings.Trim(strings.ToLower(provider), "/ ")
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return model
	}
	// Compare case-insensitively so an already-qualified model with different
	// casing (e.g. "OpenAI/gpt-4" with provider "openai") is not double-prefixed.
	if strings.HasPrefix(strings.ToLower(model), provider+"/") {
		return model
	}
	return provider + "/" + model
}

type AgentGatewayConfig struct {
	BaseURL      string
	APIKey       string
	ReadinessURL string
	HTTPClient   *http.Client
}

type AgentGatewayBackend struct {
	*openAICompatibleBackend
}

func NewAgentGatewayBackend(cfg AgentGatewayConfig) *AgentGatewayBackend {
	return &AgentGatewayBackend{
		openAICompatibleBackend: newOpenAICompatibleBackend(openAICompatibleConfig{
			Name:           BackendAgentGateway,
			DisplayName:    "agentgateway",
			BaseURL:        cfg.BaseURL,
			APIKey:         cfg.APIKey,
			HealthURL:      cfg.ReadinessURL,
			ProviderHeader: "X-AI-Orch-Provider",
			ResolveModel: func(_ string, model string) string {
				return strings.TrimSpace(model)
			},
			HTTPClient: cfg.HTTPClient,
		}),
	}
}

type openAICompatibleConfig struct {
	Name           string
	DisplayName    string
	BaseURL        string
	APIKey         string
	HealthURL      string
	ProviderHeader string
	ResolveModel   func(provider string, model string) string
	HTTPClient     *http.Client
}

type openAICompatibleBackend struct {
	name           string
	displayName    string
	baseURL        string
	apiKey         string
	healthURL      string
	providerHeader string
	resolveModel   func(provider string, model string) string
	httpClient     *http.Client
	streamHTTP     *http.Client
}

func newOpenAICompatibleBackend(cfg openAICompatibleConfig) *openAICompatibleBackend {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	streamHTTP := cfg.HTTPClient
	if streamHTTP == nil {
		streamHTTP = &http.Client{Timeout: 0}
	}
	displayName := strings.TrimSpace(cfg.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(cfg.Name)
	}
	return &openAICompatibleBackend{
		name:           strings.TrimSpace(cfg.Name),
		displayName:    displayName,
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:         cfg.APIKey,
		healthURL:      strings.TrimSpace(cfg.HealthURL),
		providerHeader: strings.TrimSpace(cfg.ProviderHeader),
		resolveModel:   cfg.ResolveModel,
		httpClient:     httpClient,
		streamHTTP:     streamHTTP,
	}
}

func (b *openAICompatibleBackend) Name() string {
	if b == nil {
		return ""
	}
	return b.name
}

func (b *openAICompatibleBackend) ResolvedModel(provider string, model string) string {
	if b != nil && b.resolveModel != nil {
		return b.resolveModel(provider, model)
	}
	return strings.TrimSpace(model)
}

func (b *openAICompatibleBackend) ChatCompletion(ctx context.Context, req openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	if b == nil || b.baseURL == "" {
		return openrouter.ChatCompletionResponse{}, fmt.Errorf("%s backend base URL is required", b.backendLabel())
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
		return openrouter.ChatCompletionResponse{}, fmt.Errorf("call %s chat completions: %w", b.backendLabel(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return openrouter.ChatCompletionResponse{}, fmt.Errorf("%s returned %d: %s", b.backendLabel(), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result openrouter.ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return openrouter.ChatCompletionResponse{}, fmt.Errorf("decode %s response: %w", b.backendLabel(), err)
	}
	return result, nil
}

func (b *openAICompatibleBackend) ChatCompletionStream(ctx context.Context, req openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	if b == nil || b.baseURL == "" {
		return nil, fmt.Errorf("%s backend base URL is required", b.backendLabel())
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
		return nil, fmt.Errorf("call %s chat completions stream: %w", b.backendLabel(), err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("%s returned %d: %s", b.backendLabel(), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.Body, nil
}

func (b *openAICompatibleBackend) Health(ctx context.Context) error {
	if b == nil || b.healthURL == "" {
		return nil
	}
	req, err := b.newRequest(ctx, http.MethodGet, b.healthURL, nil, "")
	if err != nil {
		return err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s health check failed: %w", b.backendLabel(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s health returned %d", b.backendLabel(), resp.StatusCode)
	}
	return nil
}

func (b *openAICompatibleBackend) encodeRequest(req openrouter.ChatCompletionRequest) ([]byte, error) {
	req.Model = b.ResolvedModel(req.Provider, req.Model)
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", b.backendLabel(), err)
	}
	return body, nil
}

func (b *openAICompatibleBackend) newRequest(ctx context.Context, method string, url string, body io.Reader, provider string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", b.backendLabel(), err)
	}
	req.Header.Set("Content-Type", "application/json")
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	if provider != "" && b.providerHeader != "" {
		req.Header.Set(b.providerHeader, provider)
	}
	return req, nil
}

func (b *openAICompatibleBackend) backendLabel() string {
	if b == nil || b.displayName == "" {
		return "OpenAI-compatible backend"
	}
	return b.displayName
}
