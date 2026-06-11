package appconfig

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/envx"
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
	ModelBackend                string // bifrost or copilot-user.
	BifrostBaseURL              string // Base URL for the Bifrost sidecar when selected.
	BifrostAPIKey               string // Optional Bifrost bearer token if sidecar auth is enabled.
	EnableServerContextResolver bool   // Local-dev only git context fallback.
	RequireWorkItem             bool   // Require a branch or explicit work item ID before creating governed sessions.
	BackendControlEnabled       bool   // Allow admin UI to run docker compose backend controls.
	BackendControlWorkDir       string // Directory containing docker-compose files for backend controls.
	TrustedClientToken          string // Shared secret that gates privileged audit trust levels (gateway_enforced, managed_client).
	Environment                 string // Deployment posture: local (default) or production.
	GatewayMaxRequestBytes      int    // Max request body size accepted by the model compatibility gateway.
	RequireBackendHealth        bool   // Refuse to start when the model backend is unhealthy instead of degrading.
	GatewayAutoSession          bool   // Allow runtime model calls without explicit session headers by creating an auto session.
}

// IsProduction reports whether the shell runs with the production posture,
// which makes weak local-dev defaults fail closed at startup.
func (c Config) IsProduction() bool {
	return c.Environment == "production"
}

// localDefaultTokens are the well-known Compose defaults that must never reach
// a production deployment.
var localDefaultTokens = map[string]string{
	"AI_ORCH_DEV_TOKEN":            "local-dev",
	"AI_ORCH_ADMIN_TOKEN":          "local-admin",
	"AI_ORCH_SERVICE_TOKEN":        "local-service-token",
	"AI_ORCH_RUNTIME_TOKEN":        "local-runtime-token",
	"AI_ORCH_TRUSTED_CLIENT_TOKEN": "local-trusted-client-token",
}

// ValidateProduction returns the configuration problems that make this config
// unsafe to run with AI_ORCH_ENV=production. An empty slice means safe.
func (c Config) ValidateProduction() []string {
	if !c.IsProduction() {
		return nil
	}
	var problems []string
	check := func(name, value string) {
		if value == "" {
			problems = append(problems, name+" must be set in production")
			return
		}
		if def, ok := localDefaultTokens[name]; ok && value == def {
			problems = append(problems, name+" must not use the local default value in production")
		}
	}
	// The dev token is ignored in production (OIDC is mandatory), so it is
	// only rejected when it carries a known local default that suggests a
	// copied dev configuration.
	if c.DevToken == localDefaultTokens["AI_ORCH_DEV_TOKEN"] {
		problems = append(problems, "AI_ORCH_DEV_TOKEN must not use the local default value in production")
	}
	check("AI_ORCH_ADMIN_TOKEN", c.AdminToken)
	check("AI_ORCH_SERVICE_TOKEN", c.ServiceToken)
	if c.RuntimeToken != "" {
		check("AI_ORCH_RUNTIME_TOKEN", c.RuntimeToken)
	}
	check("AI_ORCH_TRUSTED_CLIENT_TOKEN", c.TrustedClientToken)
	return problems
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
	gatewayMaxRequestBytes, err := envInt("AI_ORCH_GATEWAY_MAX_REQUEST_BYTES", 20<<20)
	if err != nil {
		return Config{}, err
	}
	requireBackendHealth, err := envBool("AI_ORCH_REQUIRE_BACKEND_HEALTH", false)
	if err != nil {
		return Config{}, err
	}
	gatewayAutoSession, err := envBool("AI_ORCH_GATEWAY_AUTO_SESSION", true)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Addr:                        envx.OrDefault("AI_ORCH_ADDR", ":8080"),
		CatalogRoot:                 envx.OrDefault("AI_ORCH_CATALOG_ROOT", "."),
		AuditPath:                   envx.OrDefault("AI_ORCH_AUDIT_PATH", "var/audit/audit.jsonl"),
		DevToken:                    envx.OrDefault("AI_ORCH_DEV_TOKEN", ""),
		AdminToken:                  envx.OrDefault("AI_ORCH_ADMIN_TOKEN", ""),
		ServiceToken:                envx.OrDefault("AI_ORCH_SERVICE_TOKEN", ""),
		RuntimeToken:                envx.OrDefault("AI_ORCH_RUNTIME_TOKEN", ""),
		ClassificationMax:           envx.OrDefault("AI_ORCH_CLASSIFICATION_MAX", "internal"),
		KillSwitch:                  killSwitch,
		CostCapEnabled:              costCapEnabled,
		SessionCostCapUSD:           sessionCostCapUSD,
		PolicyEngine:                envx.OrDefault("AI_ORCH_POLICY_ENGINE", "native"),
		ToolLoopMax:                 toolLoopMax,
		GatewayAddr:                 envx.OrDefault("AI_ORCH_GATEWAY_ADDR", ":18082"),
		ModelBackend:                envx.OrDefault("AI_ORCH_MODEL_BACKEND", "bifrost"),
		BifrostBaseURL:              envx.OrDefault("AI_ORCH_BIFROST_BASE_URL", ""),
		BifrostAPIKey:               envx.OrDefault("AI_ORCH_BIFROST_API_KEY", ""),
		EnableServerContextResolver: enableServerContextResolver,
		RequireWorkItem:             requireWorkItem,
		BackendControlEnabled:       backendControlEnabled,
		BackendControlWorkDir:       envx.OrDefault("AI_ORCH_BACKEND_CONTROL_WORKDIR", "."),
		TrustedClientToken:          envx.OrDefault("AI_ORCH_TRUSTED_CLIENT_TOKEN", ""),
		Environment:                 envx.OrDefault("AI_ORCH_ENV", "local"),
		GatewayMaxRequestBytes:      gatewayMaxRequestBytes,
		RequireBackendHealth:        requireBackendHealth,
		GatewayAutoSession:          gatewayAutoSession,
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
	fs.StringVar(&cfg.ModelBackend, "model-backend", cfg.ModelBackend, "model backend adapter (bifrost or copilot-user)")
	fs.StringVar(&cfg.BifrostBaseURL, "bifrost-base-url", cfg.BifrostBaseURL, "Bifrost sidecar base URL")
	fs.StringVar(&cfg.BifrostAPIKey, "bifrost-api-key", cfg.BifrostAPIKey, "optional Bifrost sidecar bearer token")
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
