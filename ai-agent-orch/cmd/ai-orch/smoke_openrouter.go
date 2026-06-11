package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/envx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
)

// handleOpenRouterSmoke sends one direct OpenRouter completion to prove the
// provider key and model alias work. Invoked as: ai-orch smoke openrouter
func handleOpenRouterSmoke(args []string) {
	fs := flag.NewFlagSet("smoke openrouter", flag.ExitOnError)
	catalogRoot := fs.String("catalog-root", envx.OrDefault("AI_ORCH_CATALOG_ROOT", "."), "catalog root directory")
	modelAlias := fs.String("model-alias", envx.OrDefault("OPENROUTER_MODEL_ALIAS", "smoke-deepseek-v4-flash"), "model registry alias to smoke test")
	modelID := fs.String("model-id", envx.OrDefault("OPENROUTER_MODEL", ""), "explicit OpenRouter model ID; overrides model alias")
	prompt := fs.String("prompt", "Reply with exactly: smoke-ok", "smoke-test prompt")
	expected := fs.String("expect", "smoke-ok", "expected assistant response; set empty to skip content assertion")
	maxTokens := fs.Int("max-tokens", 64, "maximum response tokens")
	baseURL := fs.String("base-url", envx.OrDefault("OPENROUTER_BASE_URL", openrouter.DefaultBaseURL), "OpenRouter API base URL")
	_ = fs.Parse(args)

	resolvedModel := *modelID
	if resolvedModel == "" {
		var err error
		resolvedModel, err = catalog.ResolveOpenRouterModelID(*catalogRoot, *modelAlias)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve model alias: %v\n", err)
			os.Exit(1)
		}
	}

	client := openrouter.NewClient(openrouter.Config{
		APIKey:   os.Getenv("OPENROUTER_API_KEY"),
		BaseURL:  *baseURL,
		Referer:  os.Getenv("OPENROUTER_HTTP_REFERER"),
		AppTitle: envx.OrDefault("OPENROUTER_APP_TITLE", "ai-agent-orch-local"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	response, err := client.ChatCompletion(ctx, openrouter.ChatCompletionRequest{
		Model: resolvedModel,
		Messages: []openrouter.Message{
			{Role: "user", Content: *prompt},
		},
		Temperature: 0,
		MaxTokens:   *maxTokens,
		Reasoning: &openrouter.ReasoningConfig{
			Exclude: true,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "openrouter smoke failed: %v\n", err)
		os.Exit(1)
	}

	content := response.FirstContent()
	if err := validateSmokeResponse(content, *expected); err != nil {
		fmt.Fprintf(os.Stderr, "openrouter smoke failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("model: %s\n", resolvedModel)
	fmt.Printf("response: %s\n", content)
	if response.Usage.TotalTokens > 0 {
		fmt.Printf("usage: prompt=%d completion=%d total=%d\n", response.Usage.PromptTokens, response.Usage.CompletionTokens, response.Usage.TotalTokens)
	}
}

func validateSmokeResponse(content string, expected string) error {
	actual := strings.TrimSpace(content)
	want := strings.TrimSpace(expected)
	if actual == "" {
		return fmt.Errorf("assistant response content was empty")
	}
	if want != "" && actual != want {
		return fmt.Errorf("assistant response mismatch: expected %q, got %q", want, actual)
	}
	return nil
}
