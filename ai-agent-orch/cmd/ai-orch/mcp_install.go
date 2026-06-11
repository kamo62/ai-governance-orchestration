package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/skillsfactory"
)

func handleMCPInstall(cfg Config, args []string) {
	fs := flag.NewFlagSet("mcp install", flag.ExitOnError)
	clientFlag := fs.String("client", "", "Client to install for (vscode, cline, claude-code, codex)")
	force := fs.Bool("force", false, "Overwrite existing client configuration files")
	_ = fs.Parse(args)

	if *clientFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: ai-orch mcp install --client <vscode|cline|claude-code|codex> [--force]")
		os.Exit(1)
	}

	client, err := skillsfactory.ParseClientType(*clientFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid client: %v\n", err)
		os.Exit(1)
	}

	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory: %v\n", err)
		os.Exit(1)
	}

	result, err := skillsfactory.InstallWithOptions(client, dir, cfg.GovernanceURL, skillsfactory.InstallOptions{Force: *force})
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Files written:")
	for _, f := range result.FilesWritten {
		fmt.Printf("  %s\n", f)
	}
	fmt.Println()
	fmt.Println(result.Instructions)
}

func handleMCPDoctor(ctx context.Context, cfg Config, args []string) {
	fs := flag.NewFlagSet("mcp doctor", flag.ExitOnError)
	_ = fs.Parse(args)

	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory: %v\n", err)
		os.Exit(1)
	}

	issues := skillsfactory.Doctor(dir, cfg.GovernanceURL)
	fmt.Println("MCP Doctor — Client Config Check")
	fmt.Println()
	for _, issue := range issues {
		fmt.Printf("- %s\n", issue)
	}
	fmt.Println()
	fmt.Println("MCP Doctor — Runtime Health Check")
	fmt.Println()
	for _, check := range runtimeDoctorChecks(ctx, cfg, &http.Client{Timeout: 5 * time.Second}) {
		fmt.Printf("- %s\n", check)
	}
}

func runtimeDoctorChecks(ctx context.Context, cfg Config, client *http.Client) []string {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	var checks []string
	if cfg.Token == "" {
		checks = append(checks, "Developer token: MISSING (set AI_ORCH_DEV_TOKEN for MCP tools)")
	} else {
		checks = append(checks, "Developer token: OK")
	}
	if cfg.RuntimeToken == "" {
		checks = append(checks, "Runtime token: MISSING (set AI_ORCH_RUNTIME_TOKEN for model gateway checks)")
	} else {
		checks = append(checks, "Runtime token: OK")
	}

	readyURL := strings.TrimRight(cfg.GovernanceURL, "/") + "/readyz"
	body, err := doctorGET(ctx, client, readyURL, "")
	if err != nil {
		checks = append(checks, fmt.Sprintf("Governance Shell readyz: FAIL (%v)", err))
	} else if !strings.Contains(body, "governance-shell") {
		checks = append(checks, "Governance Shell readyz: WARN (reachable but service identity did not mention governance-shell)")
	} else {
		checks = append(checks, "Governance Shell readyz: OK")
	}

	if cfg.RuntimeToken == "" {
		checks = append(checks, "Model gateway: SKIP (AI_ORCH_RUNTIME_TOKEN not set)")
		return checks
	}
	modelsURL := strings.TrimRight(cfg.ModelGatewayURL, "/") + "/v1/models?classification=internal"
	if _, err := doctorGET(ctx, client, modelsURL, cfg.RuntimeToken); err != nil {
		checks = append(checks, fmt.Sprintf("Model gateway: FAIL (%v)", err))
	} else {
		checks = append(checks, "Model gateway: OK")
	}
	return checks
}

func doctorGET(ctx context.Context, client *http.Client, url string, bearerToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("%s returned %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}
