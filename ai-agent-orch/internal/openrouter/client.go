package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://openrouter.ai/api/v1"

type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	Referer    string
	AppTitle   string
}

type Client struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	streamClient *http.Client
	referer      string
	appTitle     string
}

type ChatClient interface {
	ChatCompletion(context.Context, ChatCompletionRequest) (ChatCompletionResponse, error)
	ChatCompletionStream(context.Context, ChatCompletionRequest) (io.ReadCloser, error)
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatCompletionRequest struct {
	SessionID   string           `json:"-"`
	ModelAlias  string           `json:"-"`
	Provider    string           `json:"-"`
	Model       string           `json:"model"`
	Messages    []Message        `json:"messages"`
	Temperature float64          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Reasoning   *ReasoningConfig `json:"reasoning,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
}

type ReasoningConfig struct {
	Effort  string `json:"effort,omitempty"`
	Exclude bool   `json:"exclude,omitempty"`
}

type ChatCompletionResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

type Usage struct {
	PromptTokens            int     `json:"prompt_tokens"`
	CompletionTokens        int     `json:"completion_tokens"`
	TotalTokens             int     `json:"total_tokens"`
	Cost                    float64 `json:"cost,omitempty"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	} `json:"completion_tokens_details,omitempty"`
}

func (u *Usage) UnmarshalJSON(data []byte) error {
	type usageAlias struct {
		PromptTokens            int             `json:"prompt_tokens"`
		CompletionTokens        int             `json:"completion_tokens"`
		TotalTokens             int             `json:"total_tokens"`
		Cost                    json.RawMessage `json:"cost,omitempty"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens,omitempty"`
		} `json:"completion_tokens_details,omitempty"`
	}
	var raw usageAlias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	u.PromptTokens = raw.PromptTokens
	u.CompletionTokens = raw.CompletionTokens
	u.TotalTokens = raw.TotalTokens
	u.CompletionTokensDetails = raw.CompletionTokensDetails
	if len(raw.Cost) > 0 {
		u.Cost = parseCost(raw.Cost)
	}
	return nil
}

func parseCost(raw json.RawMessage) float64 {
	var asNumber float64
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return asNumber
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if n, err := strconv.ParseFloat(strings.TrimSpace(asString), 64); err == nil {
			return n
		}
		return 0
	}
	var asObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asObject); err != nil {
		return 0
	}
	for _, key := range []string{"total", "total_cost", "cost", "cost_usd", "amount"} {
		if value, ok := asObject[key]; ok {
			if n := parseCost(value); n != 0 {
				return n
			}
		}
	}
	var sum float64
	for _, value := range asObject {
		sum += parseCost(value)
	}
	return sum
}

func NewClient(cfg Config) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	streamClient := cfg.HTTPClient
	if streamClient == nil {
		streamClient = &http.Client{Timeout: 0}
	}
	return &Client{
		apiKey:       cfg.APIKey,
		baseURL:      baseURL,
		httpClient:   httpClient,
		streamClient: streamClient,
		referer:      cfg.Referer,
		appTitle:     cfg.AppTitle,
	}
}

func (c *Client) ChatCompletion(ctx context.Context, request ChatCompletionRequest) (ChatCompletionResponse, error) {
	if c.apiKey == "" {
		return ChatCompletionResponse{}, errors.New("OPENROUTER_API_KEY is required")
	}
	if request.Model == "" {
		return ChatCompletionResponse{}, errors.New("model is required")
	}
	if len(request.Messages) == 0 {
		return ChatCompletionResponse{}, errors.New("at least one message is required")
	}

	body, err := json.Marshal(request)
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("encode chat completion request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("create chat completion request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if c.referer != "" {
		httpReq.Header.Set("HTTP-Referer", c.referer)
	}
	if c.appTitle != "" {
		httpReq.Header.Set("X-Title", c.appTitle)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("call OpenRouter chat completions: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return ChatCompletionResponse{}, fmt.Errorf("OpenRouter returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var result ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ChatCompletionResponse{}, fmt.Errorf("decode OpenRouter response: %w", err)
	}
	return result, nil
}

func (r ChatCompletionResponse) FirstContent() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

// ChatCompletionStream initiates a streaming chat completion and returns an
// io.ReadCloser of server-sent events. The caller is responsible for closing
// the reader.
func (c *Client) ChatCompletionStream(ctx context.Context, request ChatCompletionRequest) (io.ReadCloser, error) {
	if c.apiKey == "" {
		return nil, errors.New("OPENROUTER_API_KEY is required")
	}
	if request.Model == "" {
		return nil, errors.New("model is required")
	}
	if len(request.Messages) == 0 {
		return nil, errors.New("at least one message is required")
	}
	request.Stream = true
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode chat completion request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create chat completion request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.referer != "" {
		httpReq.Header.Set("HTTP-Referer", c.referer)
	}
	if c.appTitle != "" {
		httpReq.Header.Set("X-Title", c.appTitle)
	}

	resp, err := c.streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call OpenRouter chat completions stream: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("OpenRouter returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp.Body, nil
}

// StreamChunk is a single SSE chunk from a streaming chat completion.
type StreamChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

// DecodeStreamChunk parses a single SSE data line into a StreamChunk.
func DecodeStreamChunk(line string) (StreamChunk, error) {
	const prefix = "data:"
	if !strings.HasPrefix(line, prefix) {
		return StreamChunk{}, fmt.Errorf("not a data line")
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if data == "[DONE]" {
		return StreamChunk{}, io.EOF
	}
	var chunk StreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return StreamChunk{}, fmt.Errorf("decode chunk: %w", err)
	}
	return chunk, nil
}
