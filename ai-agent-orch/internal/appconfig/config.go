package appconfig

import (
	"flag"
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Addr              string
	CatalogRoot       string
	AuditPath         string
	DevToken          string
	ClassificationMax string
	KillSwitch        bool
	CostCapEnabled    bool
	SessionCostCapUSD float64
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
	cfg := Config{
		Addr:              envOrDefault("AI_ORCH_ADDR", ":8080"),
		CatalogRoot:       envOrDefault("AI_ORCH_CATALOG_ROOT", "."),
		AuditPath:         envOrDefault("AI_ORCH_AUDIT_PATH", "var/audit/audit.jsonl"),
		DevToken:          envOrDefault("AI_ORCH_DEV_TOKEN", ""),
		ClassificationMax: envOrDefault("AI_ORCH_CLASSIFICATION_MAX", "internal"),
		KillSwitch:        killSwitch,
		CostCapEnabled:    costCapEnabled,
		SessionCostCapUSD: sessionCostCapUSD,
	}

	fs := flag.NewFlagSet("ai-agent-orch", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.StringVar(&cfg.CatalogRoot, "catalog-root", cfg.CatalogRoot, "catalog root directory")
	fs.StringVar(&cfg.AuditPath, "audit-path", cfg.AuditPath, "append-only JSONL audit path")
	fs.StringVar(&cfg.DevToken, "dev-token", cfg.DevToken, "local development bearer token")
	fs.StringVar(&cfg.ClassificationMax, "classification-max", cfg.ClassificationMax, "maximum allowed classification")
	fs.BoolVar(&cfg.KillSwitch, "kill-switch", cfg.KillSwitch, "block new session creation")
	fs.BoolVar(&cfg.CostCapEnabled, "cost-cap-enabled", cfg.CostCapEnabled, "enforce the session cost cap")
	fs.Float64Var(&cfg.SessionCostCapUSD, "session-cost-cap-usd", cfg.SessionCostCapUSD, "maximum estimated cost per session")
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
