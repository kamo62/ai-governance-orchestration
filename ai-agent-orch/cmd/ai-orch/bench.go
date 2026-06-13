package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type BenchmarkOptions struct {
	Workflow string
	Models   string
}

type BenchmarkResult struct {
	Workflow string                 `json:"workflow"`
	Results  []BenchmarkModelResult `json:"results"`
}

type BenchmarkModelResult struct {
	ModelAlias        string  `json:"model_alias"`
	Passed            bool    `json:"passed"`
	Score             float64 `json:"score"`
	LatencyMillis     int64   `json:"latency_ms"`
	PromptTokens      int     `json:"prompt_tokens,omitempty"`
	CompletionTokens  int     `json:"completion_tokens,omitempty"`
	TotalTokens       int     `json:"total_tokens,omitempty"`
	CostUSD           float64 `json:"cost_usd,omitempty"`
	CostPerSuccessUSD float64 `json:"cost_per_success_usd"`
	SessionID         string  `json:"session_id,omitempty"`
	Error             string  `json:"error,omitempty"`
}

func handleBench(ctx context.Context, cfg Config, args []string) {
	if len(args) < 1 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: ai-orch bench run --workflow <workflow> --models all-enabled")
		os.Exit(1)
	}
	fs := flag.NewFlagSet("bench run", flag.ContinueOnError)
	workflow := fs.String("workflow", "smoke", "benchmark workflow")
	models := fs.String("models", "all-enabled", "all-enabled or comma-separated model aliases")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(1)
	}
	result, err := runBenchmark(ctx, cfg, BenchmarkOptions{Workflow: *workflow, Models: *models})
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchmark failed: %v\n", err)
		os.Exit(2)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

func runBenchmark(ctx context.Context, cfg Config, opts BenchmarkOptions) (BenchmarkResult, error) {
	workflow := strings.TrimSpace(opts.Workflow)
	if workflow == "" {
		workflow = "smoke"
	}
	models, err := benchmarkModels(ctx, cfg, opts.Models)
	if err != nil {
		return BenchmarkResult{}, err
	}
	if len(models) == 0 {
		return BenchmarkResult{}, errors.New("no models available for benchmark")
	}
	result := BenchmarkResult{Workflow: workflow}
	for _, model := range models {
		item := runBenchmarkModel(ctx, cfg, workflow, model)
		result.Results = append(result.Results, item)
		_ = postBenchmarkEvidence(ctx, cfg, workflow, item)
	}
	return result, nil
}

func benchmarkModels(ctx context.Context, cfg Config, models string) ([]string, error) {
	if strings.TrimSpace(models) != "" && strings.TrimSpace(models) != "all-enabled" {
		parts := strings.Split(models, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if model := strings.TrimSpace(part); model != "" {
				out = append(out, model)
			}
		}
		return out, nil
	}
	discovered, err := fetchGatewayModelsForOpenCode(ctx, cfg.ModelGatewayURL, cfg.RuntimeToken, localIdentity(), "internal")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(discovered))
	for _, model := range discovered {
		out = append(out, model.ID)
	}
	return out, nil
}

func runBenchmarkModel(ctx context.Context, cfg Config, workflow string, model string) BenchmarkModelResult {
	started := time.Now()
	item := BenchmarkModelResult{ModelAlias: model}
	prompt := benchmarkPrompt(workflow)
	body, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": prompt}}, "stream": false})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openCodeBaseURL(cfg.ModelGatewayURL)+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		item.Error = err.Error()
		return item
	}
	req.Header.Set("Authorization", "Bearer "+cfg.RuntimeToken)
	req.Header.Set("Content-Type", "application/json")
	if actor := localIdentity(); actor != "" {
		req.Header.Set("X-AI-Orch-Actor-Subject", actor)
	}
	req.Header.Set("X-AI-Orch-Client", "ai-orch-bench")
	resp, err := http.DefaultClient.Do(req)
	item.LatencyMillis = time.Since(started).Milliseconds()
	if err != nil {
		item.Error = err.Error()
		return item
	}
	defer resp.Body.Close()
	item.SessionID = resp.Header.Get("X-AI-Orch-Session-ID")
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		item.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		return item
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	_ = json.Unmarshal(respBody, &parsed)
	content := ""
	if len(parsed.Choices) > 0 {
		content = strings.TrimSpace(parsed.Choices[0].Message.Content)
	}
	item.Passed = content != ""
	if item.Passed {
		item.Score = 1
	}
	item.PromptTokens = intFromUsage(parsed.Usage, "prompt_tokens")
	item.CompletionTokens = intFromUsage(parsed.Usage, "completion_tokens")
	item.TotalTokens = intFromUsage(parsed.Usage, "total_tokens")
	item.CostUSD = floatFromUsage(parsed.Usage, "cost_usd")
	if item.Passed && item.CostUSD > 0 {
		item.CostPerSuccessUSD = item.CostUSD
	}
	return item
}

func benchmarkPrompt(workflow string) string {
	switch strings.TrimSpace(strings.ToLower(workflow)) {
	case "smoke", "":
		return "Reply with OK and one short reason. This is an AI-Orch model benchmark smoke workflow."
	default:
		return "Run the AI-Orch benchmark workflow " + workflow + " and return a concise result with evidence."
	}
}

func postBenchmarkEvidence(ctx context.Context, cfg Config, workflow string, result BenchmarkModelResult) error {
	if result.SessionID == "" {
		return nil
	}
	desc, _ := json.Marshal(result)
	body, _ := json.Marshal(map[string]any{
		"session_id":       result.SessionID,
		"evidence_type":    "model_benchmark",
		"description":      "AI-Orch benchmark " + workflow + " result for " + result.ModelAlias + ": " + string(desc),
		"test_result":      map[bool]string{true: "passed", false: "failed"}[result.Passed],
		"trust_level":      "gateway_enforced",
		"enforcement_mode": "benchmark",
	})
	_, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/evidence", body)
	return err
}

func intFromUsage(usage map[string]any, key string) int {
	if usage == nil {
		return 0
	}
	switch v := usage[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func floatFromUsage(usage map[string]any, key string) float64 {
	if usage == nil {
		return 0
	}
	switch v := usage[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}
