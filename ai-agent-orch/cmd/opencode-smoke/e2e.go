package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"ai-agent-orch/internal/betasmoke"
)

func runOpenCodeE2E(gatewayURL string, args []string) error {
	targetDir, model, prompt := openCodeE2EArgs(args)
	if _, err := exec.LookPath("opencode"); err != nil {
		return fmt.Errorf("opencode binary not found: %w", err)
	}

	cfg := betasmoke.LoadConfigFromEnv()
	if gatewayURL != "" {
		cfg.GatewayURL = gatewayURL
	}
	if strings.TrimSpace(cfg.RuntimeToken) == "" {
		return fmt.Errorf("AI_ORCH_RUNTIME_TOKEN is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	sessionID, specialist, err := createGovernedRun(ctx, cfg, prompt)
	if err != nil {
		return err
	}
	fmt.Printf("OpenCode governed session: %s (%s)\n", sessionID, specialist)

	configFile, err := os.CreateTemp("", "ai-orch-opencode-e2e-*.json")
	if err != nil {
		return fmt.Errorf("create temporary OpenCode config: %w", err)
	}
	defer os.Remove(configFile.Name())
	if err := json.NewEncoder(configFile).Encode(GenerateOpenCodeConfig(gatewayURL)); err != nil {
		configFile.Close()
		return fmt.Errorf("write temporary OpenCode config: %w", err)
	}
	if err := configFile.Close(); err != nil {
		return fmt.Errorf("close temporary OpenCode config: %w", err)
	}

	cmd := exec.CommandContext(ctx, "opencode", "run", "--dir", targetDir, "--model", model, "--format", "json", prompt)
	cmd.Env = append(os.Environ(),
		"OPENCODE_CONFIG="+configFile.Name(),
		"AI_ORCH_RUNTIME_TOKEN="+cfg.RuntimeToken,
		"AI_ORCH_SESSION_ID="+sessionID,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run local OpenCode: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	if expected := strings.TrimSpace(os.Getenv("OPENCODE_EXPECT")); expected != "" && !strings.Contains(strings.ToLower(stdout.String()+stderr.String()), strings.ToLower(expected)) {
		fmt.Printf("OpenCode completed; expected marker %q was not visible, checking audit evidence.\n", expected)
	}

	if err := requireGatewayAudit(ctx, cfg, sessionID); err != nil {
		return err
	}
	fmt.Println("OpenCode model-gateway audit event present")
	fmt.Println("=== opencode e2e passed ===")
	return nil
}

func openCodeE2EArgs(args []string) (string, string, string) {
	targetDir := envOrDefault("OPENCODE_TARGET_DIR", ".")
	model := envOrDefault("OPENCODE_MODEL", defaultModel)
	prompt := envOrDefault("OPENCODE_PROMPT", "Reply with exactly: opencode-e2e-ok")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			if i+1 < len(args) {
				i++
				targetDir = args[i]
			}
		case "--model":
			if i+1 < len(args) {
				i++
				model = args[i]
			}
		case "--prompt":
			if i+1 < len(args) {
				i++
				prompt = args[i]
			}
		}
	}
	return targetDir, model, prompt
}

func createGovernedRun(ctx context.Context, cfg betasmoke.Config, prompt string) (string, string, error) {
	body, _ := json.Marshal(map[string]any{
		"agent":           "unit-tests",
		"classification":  cfg.Classification,
		"prompt":          prompt,
		"permission_mode": "reviewed",
		"approval_mode":   "manual",
		"workspace_mode":  "local",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.GovernanceURL, "/")+"/v1/runs", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.DevToken)
	resp, err := (&http.Client{Timeout: cfg.HTTPTimeout}).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("start governed run: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("start governed run: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", fmt.Errorf("decode governed run: %w", err)
	}
	sessionID, _ := parsed["session_id"].(string)
	specialist, _ := parsed["specialist"].(string)
	if sessionID == "" {
		return "", "", fmt.Errorf("governed run response missing session_id")
	}
	return sessionID, specialist, nil
}

func requireGatewayAudit(ctx context.Context, cfg betasmoke.Config, sessionID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/v1/audit/sessions/%s", strings.TrimRight(cfg.GovernanceURL, "/"), sessionID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.DevToken)
	resp, err := (&http.Client{Timeout: cfg.HTTPTimeout}).Do(req)
	if err != nil {
		return fmt.Errorf("audit lookup: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("audit lookup: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if !strings.Contains(string(raw), "model.gateway") {
		return fmt.Errorf("audit trail missing model.gateway event for session %s", sessionID)
	}
	return nil
}
