package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultGitHubBaseURL  = "https://github.com"
	DefaultGitHubAPIURL   = "https://api.github.com"
	DefaultCopilotBaseURL = "https://api.githubcopilot.com"
	DefaultClientID       = "Iv1.b507a08c87ecfe98"
	DefaultScope          = "read:user"
)

type Client struct {
	HTTPClient     *http.Client
	GitHubBaseURL  string
	GitHubAPIURL   string
	CopilotBaseURL string
	ClientID       string
	UserAgent      string
}

func NewClient() *Client {
	return &Client{
		HTTPClient:     &http.Client{Timeout: 45 * time.Second},
		GitHubBaseURL:  DefaultGitHubBaseURL,
		GitHubAPIURL:   DefaultGitHubAPIURL,
		CopilotBaseURL: DefaultCopilotBaseURL,
		ClientID:       DefaultClientID,
		UserAgent:      "ai-orch/0 copilot-user",
	}
}

type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type AccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

type User struct {
	Login   string `json:"login"`
	ID      int64  `json:"id"`
	HTMLURL string `json:"html_url"`
}

func (c *Client) StartDeviceFlow(ctx context.Context) (DeviceCodeResponse, error) {
	values := url.Values{}
	values.Set("client_id", defaultString(c.ClientID, DefaultClientID))
	values.Set("scope", DefaultScope)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(defaultString(c.GitHubBaseURL, DefaultGitHubBaseURL), "/")+"/login/device/code", strings.NewReader(values.Encode()))
	if err != nil {
		return DeviceCodeResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.setUserAgent(req)
	var out DeviceCodeResponse
	if err := c.doJSON(req, &out); err != nil {
		return DeviceCodeResponse{}, err
	}
	if out.DeviceCode == "" {
		return DeviceCodeResponse{}, errors.New("device code response missing device_code")
	}
	return out, nil
}

func (c *Client) PollAccessToken(ctx context.Context, device DeviceCodeResponse) (AccessTokenResponse, error) {
	interval := time.Duration(device.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(device.ExpiresIn) * time.Second)
	for {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return AccessTokenResponse{}, errors.New("device code expired")
		}
		values := url.Values{}
		values.Set("client_id", defaultString(c.ClientID, DefaultClientID))
		values.Set("device_code", device.DeviceCode)
		values.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(defaultString(c.GitHubBaseURL, DefaultGitHubBaseURL), "/")+"/login/oauth/access_token", strings.NewReader(values.Encode()))
		if err != nil {
			return AccessTokenResponse{}, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		c.setUserAgent(req)
		var out AccessTokenResponse
		if err := c.doJSON(req, &out); err != nil {
			return AccessTokenResponse{}, err
		}
		if out.AccessToken != "" {
			return out, nil
		}
		switch out.Error {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "":
			return AccessTokenResponse{}, errors.New("access token response missing token")
		default:
			return AccessTokenResponse{}, fmt.Errorf("device auth failed: %s %s", out.Error, out.ErrorDesc)
		}
		select {
		case <-ctx.Done():
			return AccessTokenResponse{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (c *Client) User(ctx context.Context, token string) (User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(defaultString(c.GitHubAPIURL, DefaultGitHubAPIURL), "/")+"/user", nil)
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	c.setUserAgent(req)
	var out User
	return out, c.doJSON(req, &out)
}

func (c *Client) Models(ctx context.Context, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(defaultString(c.CopilotBaseURL, DefaultCopilotBaseURL), "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	c.setCopilotHeaders(req, token)
	return c.doBytes(req)
}

func (c *Client) ChatCompletion(ctx context.Context, token string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(defaultString(c.CopilotBaseURL, DefaultCopilotBaseURL), "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setCopilotHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	return c.doBytes(req)
}

func (c *Client) ChatCompletionStream(ctx context.Context, token string, body []byte) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(defaultString(c.CopilotBaseURL, DefaultCopilotBaseURL), "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setCopilotHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("copilot returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp.Body, nil
}

func (c *Client) setCopilotHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2026-06-01")
	req.Header.Set("Openai-Intent", "conversation-edits")
	req.Header.Set("x-initiator", "user")
	c.setUserAgent(req)
}

func (c *Client) setUserAgent(req *http.Request) {
	req.Header.Set("User-Agent", defaultString(c.UserAgent, "ai-orch/0 copilot-user"))
}

func (c *Client) doJSON(req *http.Request, out any) error {
	body, err := c.doBytes(req)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (c *Client) doBytes(req *http.Request) ([]byte, error) {
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %d: %s", req.URL.Host, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
