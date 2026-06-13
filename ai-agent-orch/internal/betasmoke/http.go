package betasmoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type httpClient struct {
	devToken     string
	runtimeToken string
	actorSubject string
	timeout      time.Duration
}

func (c httpClient) do(ctx context.Context, method, url, token string, body []byte) (int, []byte, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if c.actorSubject != "" {
		req.Header.Set("X-AI-Orch-Local-Identity", c.actorSubject)
	}
	client := &http.Client{Timeout: c.timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func (c httpClient) requireOK(status int, raw []byte, action string) error {
	if status >= 200 && status < 300 {
		return nil
	}
	return fmt.Errorf("%s: HTTP %d: %s", action, status, strings.TrimSpace(string(raw)))
}

func extractAssistantContent(raw []byte) (string, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("chat completion returned no choices")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func validateExpected(actual, expected string) error {
	if strings.TrimSpace(actual) == "" {
		return fmt.Errorf("assistant response was empty")
	}
	want := strings.TrimSpace(expected)
	if want != "" && actual != want {
		return fmt.Errorf("assistant response %q != expected %q", actual, want)
	}
	return nil
}
