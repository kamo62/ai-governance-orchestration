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

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/copilot"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
)

const (
	BackendBifrost = "bifrost"
)

type Backend interface {
	Name() string
	ResolvedModel(provider string, model string) string
	ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error)
	ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error)
}

// RawRequest carries an OpenAI-compatible request body that should be forwarded
// without narrowing provider-specific fields such as tools, tool calls,
// response_format, reasoning metadata, or multimodal content arrays.
type RawRequest struct {
	Provider     string
	ModelAlias   string
	Model        string
	Body         []byte
	ActorSubject string
}

// RawChatBackend is implemented by backends that can forward chat completions
// while preserving the caller's OpenAI-compatible JSON payload.
type RawChatBackend interface {
	ChatCompletionRaw(context.Context, RawRequest) ([]byte, error)
	ChatCompletionStreamRaw(context.Context, RawRequest) (io.ReadCloser, error)
}

// RawResponsesBackend is implemented by backends that can forward the OpenAI
// Responses API while preserving the caller's JSON payload and event stream.
type RawResponsesBackend interface {
	ResponsesRaw(context.Context, RawRequest) ([]byte, error)
	ResponsesStreamRaw(context.Context, RawRequest) (io.ReadCloser, error)
}

type BackendConfig struct {
	Name                 string
	BifrostBaseURL       string
	BifrostAPIKey        string
	CopilotTokenResolver CopilotTokenResolver
	HTTPClient           *http.Client
}

func New(cfg BackendConfig) (Backend, error) {
	switch strings.TrimSpace(cfg.Name) {
	case "", BackendBifrost:
		if strings.TrimSpace(cfg.BifrostBaseURL) == "" {
			return nil, errors.New("bifrost backend requires a base URL")
		}
		return NewBifrostBackend(BifrostConfig{
			BaseURL:    cfg.BifrostBaseURL,
			APIKey:     cfg.BifrostAPIKey,
			HTTPClient: cfg.HTTPClient,
		}), nil
	case BackendCopilotUser:
		if cfg.CopilotTokenResolver == nil {
			return nil, errors.New("copilot-user backend requires a token resolver")
		}
		client := copilot.NewClient()
		if cfg.HTTPClient != nil {
			client.HTTPClient = cfg.HTTPClient
		}
		return NewCopilotUserBackend(client, cfg.CopilotTokenResolver), nil
	default:
		return nil, fmt.Errorf("unknown model backend %q", cfg.Name)
	}
}

func withRawModel(body []byte, model string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("decode raw chat request: %w", err)
	}
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode raw model: %w", err)
	}
	obj["model"] = modelJSON
	encoded, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("encode raw chat request: %w", err)
	}
	return encoded, nil
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

func (b *openAICompatibleBackend) ChatCompletionRaw(ctx context.Context, req RawRequest) ([]byte, error) {
	return b.postRaw(ctx, "/v1/chat/completions", req)
}

func (b *openAICompatibleBackend) ChatCompletionStreamRaw(ctx context.Context, req RawRequest) (io.ReadCloser, error) {
	return b.streamRaw(ctx, "/v1/chat/completions", req)
}

func (b *openAICompatibleBackend) ResponsesRaw(ctx context.Context, req RawRequest) ([]byte, error) {
	return b.postRaw(ctx, "/v1/responses", req)
}

func (b *openAICompatibleBackend) ResponsesStreamRaw(ctx context.Context, req RawRequest) (io.ReadCloser, error) {
	return b.streamRaw(ctx, "/v1/responses", req)
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

func (b *openAICompatibleBackend) encodeRawRequest(req RawRequest) ([]byte, error) {
	if b == nil || b.baseURL == "" {
		return nil, fmt.Errorf("%s backend base URL is required", b.backendLabel())
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}
	if len(req.Body) == 0 {
		return nil, errors.New("request body is required")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return nil, fmt.Errorf("decode %s request body: %w", b.backendLabel(), err)
	}
	resolved := b.ResolvedModel(req.Provider, req.Model)
	modelJSON, err := json.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("encode %s model: %w", b.backendLabel(), err)
	}
	body["model"] = modelJSON
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", b.backendLabel(), err)
	}
	return encoded, nil
}

func (b *openAICompatibleBackend) postRaw(ctx context.Context, path string, req RawRequest) ([]byte, error) {
	body, err := b.encodeRawRequest(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := b.newRequest(ctx, http.MethodPost, b.baseURL+path, bytes.NewReader(body), req.Provider)
	if err != nil {
		return nil, err
	}
	resp, err := b.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call %s%s: %w", b.backendLabel(), path, err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s returned %d: %s", b.backendLabel(), resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return bodyBytes, nil
}

func (b *openAICompatibleBackend) streamRaw(ctx context.Context, path string, req RawRequest) (io.ReadCloser, error) {
	body, err := b.encodeRawRequest(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := b.newRequest(ctx, http.MethodPost, b.baseURL+path, bytes.NewReader(body), req.Provider)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := b.streamHTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call %s%s stream: %w", b.backendLabel(), path, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("%s returned %d: %s", b.backendLabel(), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.Body, nil
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
