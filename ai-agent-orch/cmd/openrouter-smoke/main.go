package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/openrouter"
)

func main() {
	catalogRoot := flag.String("catalog-root", envOrDefault("AI_ORCH_CATALOG_ROOT", "."), "catalog root directory")
	modelAlias := flag.String("model-alias", envOrDefault("OPENROUTER_MODEL_ALIAS", "smoke-deepseek-v4-flash"), "model registry alias to smoke test")
	modelID := flag.String("model-id", envOrDefault("OPENROUTER_MODEL", ""), "explicit OpenRouter model ID; overrides model alias")
	prompt := flag.String("prompt", "Reply with exactly: smoke-ok", "smoke-test prompt")
	maxTokens := flag.Int("max-tokens", 32, "maximum response tokens")
	baseURL := flag.String("base-url", envOrDefault("OPENROUTER_BASE_URL", openrouter.DefaultBaseURL), "OpenRouter API base URL")
	flag.Parse()

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
		AppTitle: envOrDefault("OPENROUTER_APP_TITLE", "ai-agent-orch-local"),
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
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "openrouter smoke failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("model: %s\n", resolvedModel)
	fmt.Printf("response: %s\n", response.FirstContent())
	if response.Usage.TotalTokens > 0 {
		fmt.Printf("usage: prompt=%d completion=%d total=%d\n", response.Usage.PromptTokens, response.Usage.CompletionTokens, response.Usage.TotalTokens)
	}
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
