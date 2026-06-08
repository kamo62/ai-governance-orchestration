package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OrchestratorHTTPClient is the concrete client that calls the Orchestrator.
type OrchestratorHTTPClient struct {
	baseURL      string
	serviceToken string
	client       *http.Client
}

func NewOrchestratorHTTPClient(baseURL string, serviceToken string) *OrchestratorHTTPClient {
	return &OrchestratorHTTPClient{
		baseURL:      baseURL,
		serviceToken: serviceToken,
		client:       &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *OrchestratorHTTPClient) Route(ctx context.Context, sessionID string, prompt string, context SessionContext) (RouteDecision, error) {
	body, _ := json.Marshal(map[string]any{"prompt": prompt, "context": context})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/orchestrator/route", bytes.NewReader(body))
	if err != nil {
		return RouteDecision{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AI-Orch-Session-ID", sessionID)
	c.setServiceAuth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return RouteDecision{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RouteDecision{}, fmt.Errorf("orchestrator returned %d", resp.StatusCode)
	}

	var result RouteDecision
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return RouteDecision{}, fmt.Errorf("decode route response: %w", err)
	}
	return result, nil
}

func (c *OrchestratorHTTPClient) AcceptSession(ctx context.Context, sessionID string, agent string) error {
	body, _ := json.Marshal(map[string]any{"agent": agent})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/orchestrator/sessions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AI-Orch-Session-ID", sessionID)
	c.setServiceAuth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("orchestrator returned %d", resp.StatusCode)
	}
	return nil
}

func (c *OrchestratorHTTPClient) Dispatch(ctx context.Context, sessionID string, agent string, prompt string) (DispatchResult, error) {
	body, _ := json.Marshal(map[string]any{"agent": agent, "prompt": prompt})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/orchestrator/dispatch", bytes.NewReader(body))
	if err != nil {
		return DispatchResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AI-Orch-Session-ID", sessionID)
	c.setServiceAuth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return DispatchResult{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DispatchResult{}, fmt.Errorf("orchestrator returned %d", resp.StatusCode)
	}

	var result DispatchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return DispatchResult{}, fmt.Errorf("decode dispatch response: %w", err)
	}
	return result, nil
}

func (c *OrchestratorHTTPClient) setServiceAuth(req *http.Request) {
	if c.serviceToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.serviceToken)
	}
}
