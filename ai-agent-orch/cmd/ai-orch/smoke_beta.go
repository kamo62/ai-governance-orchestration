package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/betasmoke"
)

// handleBetaSmoke runs the beta gateway or provider smoke suite against a
// running stack. Invoked as: ai-orch smoke gateway|provider
func handleBetaSmoke(target string) {
	cfg := betasmoke.LoadConfigFromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	var err error
	switch target {
	case "gateway":
		err = betasmoke.RunGatewaySmoke(ctx, cfg)
	case "provider":
		err = betasmoke.RunProviderSmoke(ctx, cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown smoke target %q\n", target)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s smoke failed: %v\n", target, err)
		os.Exit(1)
	}
	fmt.Printf("%s smoke passed\n", target)
}
