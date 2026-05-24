package appconfig

import "testing"

func TestLoadUsesDefaults(t *testing.T) {
	t.Setenv("AI_ORCH_ADDR", "")
	t.Setenv("AI_ORCH_CATALOG_ROOT", "")

	cfg, err := Load([]string{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("expected default addr :8080, got %q", cfg.Addr)
	}
	if cfg.CatalogRoot != "." {
		t.Fatalf("expected default catalog root ., got %q", cfg.CatalogRoot)
	}
	if cfg.AuditPath != "var/audit/audit.jsonl" {
		t.Fatalf("expected default audit path, got %q", cfg.AuditPath)
	}
	if cfg.DevToken != "" {
		t.Fatalf("expected empty default dev token, got %q", cfg.DevToken)
	}
	if cfg.ServiceToken != "" {
		t.Fatalf("expected empty default service token, got %q", cfg.ServiceToken)
	}
	if cfg.ClassificationMax != "internal" {
		t.Fatalf("expected default classification max internal, got %q", cfg.ClassificationMax)
	}
	if cfg.KillSwitch {
		t.Fatalf("expected kill switch disabled by default")
	}
	if cfg.CostCapEnabled {
		t.Fatalf("expected cost cap disabled by default")
	}
	if cfg.SessionCostCapUSD != 0 {
		t.Fatalf("expected default session cost cap 0 when disabled, got %v", cfg.SessionCostCapUSD)
	}
}

func TestLoadUsesEnvAndFlags(t *testing.T) {
	t.Setenv("AI_ORCH_ADDR", ":9000")
	t.Setenv("AI_ORCH_CATALOG_ROOT", "/tmp/catalog")
	t.Setenv("AI_ORCH_AUDIT_PATH", "/tmp/audit.jsonl")
	t.Setenv("AI_ORCH_DEV_TOKEN", "env-token")
	t.Setenv("AI_ORCH_SERVICE_TOKEN", "env-service-token")
	t.Setenv("AI_ORCH_CLASSIFICATION_MAX", "confidential")
	t.Setenv("AI_ORCH_KILL_SWITCH", "true")
	t.Setenv("AI_ORCH_COST_CAP_ENABLED", "true")
	t.Setenv("AI_ORCH_SESSION_COST_CAP_USD", "0.50")

	cfg, err := Load([]string{"-addr", ":7777", "-audit-path", "/tmp/flag-audit.jsonl", "-dev-token", "flag-token", "-service-token", "flag-service-token", "-classification-max", "restricted", "-kill-switch=false", "-cost-cap-enabled=false", "-session-cost-cap-usd", "0.75"})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Addr != ":7777" {
		t.Fatalf("expected flag addr to win, got %q", cfg.Addr)
	}
	if cfg.CatalogRoot != "/tmp/catalog" {
		t.Fatalf("expected env catalog root, got %q", cfg.CatalogRoot)
	}
	if cfg.AuditPath != "/tmp/flag-audit.jsonl" {
		t.Fatalf("expected flag audit path to win, got %q", cfg.AuditPath)
	}
	if cfg.DevToken != "flag-token" {
		t.Fatalf("expected flag dev token to win, got %q", cfg.DevToken)
	}
	if cfg.ServiceToken != "flag-service-token" {
		t.Fatalf("expected flag service token to win, got %q", cfg.ServiceToken)
	}
	if cfg.ClassificationMax != "restricted" {
		t.Fatalf("expected flag classification max to win, got %q", cfg.ClassificationMax)
	}
	if cfg.KillSwitch {
		t.Fatalf("expected flag kill switch to win")
	}
	if cfg.CostCapEnabled {
		t.Fatalf("expected flag cost cap enabled to win")
	}
	if cfg.SessionCostCapUSD != 0.75 {
		t.Fatalf("expected flag session cost cap to win, got %v", cfg.SessionCostCapUSD)
	}
}
