package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"ai-agent-orch/internal/betasmoke"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: beta-gateway-smoke <gateway|provider>")
		os.Exit(1)
	}

	cfg := betasmoke.LoadConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	var err error
	switch os.Args[1] {
	case "gateway":
		err = betasmoke.RunGatewaySmoke(ctx, cfg)
	case "provider":
		if cfg.SSETimeout < 3*time.Minute {
			cfg.SSETimeout = 3 * time.Minute
		}
		err = betasmoke.RunProviderSmoke(ctx, cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "beta-gateway-smoke failed: %v\n", err)
		os.Exit(2)
	}
}
