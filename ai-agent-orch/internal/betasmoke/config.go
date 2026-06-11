package betasmoke

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config drives beta provider and gateway smoke checks against a running stack.
type Config struct {
	GovernanceURL  string
	GatewayURL     string
	DevToken       string
	RuntimeToken   string
	ModelAlias     string
	Classification string
	Prompt         string
	Expected       string
	HTTPTimeout    time.Duration
	SSETimeout     time.Duration
	// MaxTokens must leave room for reasoning models that consume completion
	// budget on hidden thinking before any visible text.
	MaxTokens int
	// ActorSubject is asserted via X-AI-Orch-Local-Identity on dev-token
	// requests so the session actor matches per-user backends like Copilot.
	ActorSubject string
}

const (
	DefaultGovernanceURL = "http://127.0.0.1:18080"
	DefaultGatewayURL    = "http://127.0.0.1:18082"
)

func LoadConfigFromEnv() Config {
	cfg := Config{
		GovernanceURL:  envOrDefault("AI_ORCH_GOVERNANCE_URL", DefaultGovernanceURL),
		GatewayURL:     envOrDefault("AI_ORCH_MODEL_GATEWAY_URL", DefaultGatewayURL),
		DevToken:       envOrDefault("AI_ORCH_DEV_TOKEN", "local-dev"),
		RuntimeToken:   envOrDefault("AI_ORCH_RUNTIME_TOKEN", "local-runtime-token"),
		ModelAlias:     envOrDefault("AI_ORCH_GATEWAY_MODEL", "coding-fast"),
		Classification: envOrDefault("AI_ORCH_GATEWAY_CLASSIFICATION", "internal"),
		Prompt:         envOrDefault("AI_ORCH_GATEWAY_PROMPT", ""),
		Expected:       envOrDefault("AI_ORCH_GATEWAY_EXPECT", "gateway-smoke-ok"),
		HTTPTimeout:    durationEnv("AI_ORCH_SMOKE_HTTP_TIMEOUT", 45*time.Second),
		SSETimeout:     durationEnv("AI_ORCH_SMOKE_SSE_TIMEOUT", 30*time.Second),
		MaxTokens:      intEnv("AI_ORCH_GATEWAY_MAX_TOKENS", 256),
		ActorSubject:   envOrDefault("AI_ORCH_ACTOR_SUBJECT", ""),
	}
	return cfg
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if v, err := strconv.Atoi(raw); err == nil && v > 0 {
		return v
	}
	return fallback
}
