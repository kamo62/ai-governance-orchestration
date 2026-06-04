package openrouter

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
)

type ProxyConfig struct {
	BaseURL      string
	ServiceToken string
	HTTPClient   *http.Client
}

type ProxyClient struct {
	baseURL      string
	serviceToken string
	httpClient   *http.Client
}

func NewProxyClient(cfg ProxyConfig) *ProxyClient {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &ProxyClient{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		serviceToken: cfg.ServiceToken,
		httpClient:   httpClient,
	}
}

func (c *ProxyClient) ChatCompletion(ctx context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	if c.baseURL == "" {
		return ChatCompletionResponse{}, errors.New("model proxy URL is required")
	}
	if c.serviceToken == "" {
		return ChatCompletionResponse{}, errors.New("model proxy service token is required")
	}
	if request.SessionID == "" {
		return ChatCompletionResponse{}, errors.New("session ID is required for model proxy")
	}
	if request.Model == "" {
		return ChatCompletionResponse{}, errors.New("model is required")
	}
	if len(request.Messages) == 0 {
		return ChatCompletionResponse{}, errors.New("at least one message is required")
	}

	body, err := json.Marshal(request)
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("encode proxy model request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat", bytes.NewReader(body))
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("create proxy model request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.serviceToken)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-AI-Orch-Session-ID", request.SessionID)
	if request.ModelAlias != "" {
		httpReq.Header.Set("X-AI-Orch-Model-Alias", request.ModelAlias)
	}
	if request.Provider != "" {
		httpReq.Header.Set("X-AI-Orch-Provider", request.Provider)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("call model proxy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ChatCompletionResponse{}, fmt.Errorf("model proxy returned %d", resp.StatusCode)
	}

	var result ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("decode model proxy response: %w", err)
	}
	return result, nil
}

// ChatCompletionStream is not yet supported by the internal model proxy.
// It returns an error indicating that streaming should use the model compatibility gateway directly.
func (c *ProxyClient) ChatCompletionStream(_ context.Context, _ ChatCompletionRequest) (io.ReadCloser, error) {
	return nil, errors.New("streaming not supported through internal model proxy; use model compatibility gateway")
}
