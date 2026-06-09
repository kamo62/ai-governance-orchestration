package appconfig

import (
	"flag"
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Addr                        string
	CatalogRoot                 string
	AuditPath                   string
	DevToken                    string
	AdminToken                  string // Separate token for /v1/admin/* endpoints.
	ServiceToken                string
	RuntimeToken                string // Token for model compatibility gateway runtime calls.
	ClassificationMax           string
	KillSwitch                  bool
	CostCapEnabled              bool
	SessionCostCapUSD           float64
	PolicyEngine                string
	ToolLoopMax                 int
	GatewayAddr                 string // Listen address for the model compatibility gateway.
	ModelBackend                string // native-openrouter, bifrost, or agentgateway.
	BifrostBaseURL              string // Base URL for the Bifrost sidecar when selected.
	BifrostAPIKey               string // Optional Bifrost bearer token if sidecar auth is enabled.
	AgentGatewayBaseURL         string // Base URL for agentgateway data-plane traffic when selected.
	AgentGatewayAPIKey          string // Optional agentgateway bearer token if enabled.
	AgentGatewayReadinessURL    string // Optional agentgateway readiness URL, usually on the management/readiness port.
	EnableServerContextResolver bool   // Local-dev only git context fallback.
	RequireWorkItem             bool   // Require a branch or explicit work item ID before creating governed sessions.
	BackendControlEnabled       bool   // Allow admin UI to run docker compose backend controls.
	BackendControlWorkDir       string // Directory containing docker-compose files for backend controls.
	TrustedClientToken          string // Shared secret that gates privileged audit trust levels (gateway_enforced, managed_client).
}

func Load(args []string) (Config, error) {
	killSwitch, err := envBool("AI_ORCH_KILL_SWITCH", false)
	if err != nil {
		return Config{}, err
	}
	costCapEnabled, err := envBool("AI_ORCH_COST_CAP_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	sessionCostCapUSD, err := envFloat("AI_ORCH_SESSION_COST_CAP_USD", 0)
	if err != nil {
		return Config{}, err
	}
	toolLoopMax, err := envInt("AI_ORCH_CONSECUTIVE_TOOL_CALL_MAX", 15)
	if err != nil {
		return Config{}, err
	}
	enableServerContextResolver, err := envBool("AI_ORCH_ENABLE_SERVER_CONTEXT_RESOLVER", false)
	if err != nil {
		return Config{}, err
	}
	requireWorkItem, err := envBool("AI_ORCH_REQUIRE_WORK_ITEM", false)
	if err != nil {
		return Config{}, err
	}
	backendControlEnabled, err := envBool("AI_ORCH_BACKEND_CONTROL_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Addr:                        envOrDefault("AI_ORCH_ADDR", ":8080"),
		CatalogRoot:                 envOrDefault("AI_ORCH_CATALOG_ROOT", "."),
		AuditPath:                   envOrDefault("AI_ORCH_AUDIT_PATH", "var/audit/audit.jsonl"),
		DevToken:                    envOrDefault("AI_ORCH_DEV_TOKEN", ""),
		AdminToken:                  envOrDefault("AI_ORCH_ADMIN_TOKEN", ""),
		ServiceToken:                envOrDefault("AI_ORCH_SERVICE_TOKEN", ""),
		RuntimeToken:                envOrDefault("AI_ORCH_RUNTIME_TOKEN", ""),
		ClassificationMax:           envOrDefault("AI_ORCH_CLASSIFICATION_MAX", "internal"),
		KillSwitch:                  killSwitch,
		CostCapEnabled:              costCapEnabled,
		SessionCostCapUSD:           sessionCostCapUSD,
		PolicyEngine:                envOrDefault("AI_ORCH_POLICY_ENGINE", "native"),
		ToolLoopMax:                 toolLoopMax,
		GatewayAddr:                 envOrDefault("AI_ORCH_GATEWAY_ADDR", ":18082"),
		ModelBackend:                envOrDefault("AI_ORCH_MODEL_BACKEND", "native-openrouter"),
		BifrostBaseURL:              envOrDefault("AI_ORCH_BIFROST_BASE_URL", ""),
		BifrostAPIKey:               envOrDefault("AI_ORCH_BIFROST_API_KEY", ""),
		AgentGatewayBaseURL:         envOrDefault("AI_ORCH_AGENTGATEWAY_BASE_URL", ""),
		AgentGatewayAPIKey:          envOrDefault("AI_ORCH_AGENTGATEWAY_API_KEY", ""),
		AgentGatewayReadinessURL:    envOrDefault("AI_ORCH_AGENTGATEWAY_READINESS_URL", ""),
		EnableServerContextResolver: enableServerContextResolver,
		RequireWorkItem:             requireWorkItem,
		BackendControlEnabled:       backendControlEnabled,
		BackendControlWorkDir:       envOrDefault("AI_ORCH_BACKEND_CONTROL_WORKDIR", "."),
		TrustedClientToken:          envOrDefault("AI_ORCH_TRUSTED_CLIENT_TOKEN", ""),
	}

	fs := flag.NewFlagSet("ai-agent-orch", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.StringVar(&cfg.CatalogRoot, "catalog-root", cfg.CatalogRoot, "catalog root directory")
	fs.StringVar(&cfg.AuditPath, "audit-path", cfg.AuditPath, "append-only JSONL audit path")
	fs.StringVar(&cfg.DevToken, "dev-token", cfg.DevToken, "local development bearer token")
	fs.StringVar(&cfg.AdminToken, "admin-token", cfg.AdminToken, "admin bearer token for /v1/admin/* endpoints")
	fs.StringVar(&cfg.ServiceToken, "service-token", cfg.ServiceToken, "local service-to-service bearer token")
	fs.StringVar(&cfg.RuntimeToken, "runtime-token", cfg.RuntimeToken, "runtime bearer token for model compatibility gateway")
	fs.StringVar(&cfg.ClassificationMax, "classification-max", cfg.ClassificationMax, "maximum allowed classification")
	fs.StringVar(&cfg.GatewayAddr, "gateway-addr", cfg.GatewayAddr, "model compatibility gateway listen address")
	fs.BoolVar(&cfg.KillSwitch, "kill-switch", cfg.KillSwitch, "block new session creation")
	fs.BoolVar(&cfg.CostCapEnabled, "cost-cap-enabled", cfg.CostCapEnabled, "enforce the session cost cap")
	fs.Float64Var(&cfg.SessionCostCapUSD, "session-cost-cap-usd", cfg.SessionCostCapUSD, "maximum estimated cost per session")
	fs.StringVar(&cfg.PolicyEngine, "policy-engine", cfg.PolicyEngine, "policy engine adapter (native; agt is reserved)")
	fs.IntVar(&cfg.ToolLoopMax, "consecutive-tool-call-max", cfg.ToolLoopMax, "maximum consecutive tool/MCP calls before output")
	fs.StringVar(&cfg.ModelBackend, "model-backend", cfg.ModelBackend, "model backend adapter (native-openrouter, bifrost, or agentgateway)")
	fs.StringVar(&cfg.BifrostBaseURL, "bifrost-base-url", cfg.BifrostBaseURL, "Bifrost sidecar base URL")
	fs.StringVar(&cfg.BifrostAPIKey, "bifrost-api-key", cfg.BifrostAPIKey, "optional Bifrost sidecar bearer token")
	fs.StringVar(&cfg.AgentGatewayBaseURL, "agentgateway-base-url", cfg.AgentGatewayBaseURL, "agentgateway data-plane base URL")
	fs.StringVar(&cfg.AgentGatewayAPIKey, "agentgateway-api-key", cfg.AgentGatewayAPIKey, "optional agentgateway bearer token")
	fs.StringVar(&cfg.AgentGatewayReadinessURL, "agentgateway-readiness-url", cfg.AgentGatewayReadinessURL, "optional agentgateway readiness URL")
	fs.BoolVar(&cfg.EnableServerContextResolver, "enable-server-context-resolver", cfg.EnableServerContextResolver, "enable local-dev server-side git context fallback")
	fs.BoolVar(&cfg.RequireWorkItem, "require-work-item", cfg.RequireWorkItem, "require work item ID from branch or request before governed sessions")
	fs.BoolVar(&cfg.BackendControlEnabled, "backend-control-enabled", cfg.BackendControlEnabled, "allow admin UI to run docker compose backend controls")
	fs.StringVar(&cfg.BackendControlWorkDir, "backend-control-workdir", cfg.BackendControlWorkDir, "directory containing docker-compose files for backend controls")
	fs.StringVar(&cfg.TrustedClientToken, "trusted-client-token", cfg.TrustedClientToken, "shared secret that gates privileged audit trust levels")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func envFloat(key string, fallback float64) (float64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	return parsed, nil
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func envBool(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}
