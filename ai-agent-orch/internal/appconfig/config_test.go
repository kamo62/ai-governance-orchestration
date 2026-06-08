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
	if cfg.PolicyEngine != "native" {
		t.Fatalf("expected default policy engine native, got %q", cfg.PolicyEngine)
	}
	if cfg.ToolLoopMax != 15 {
		t.Fatalf("expected default tool loop max 15, got %d", cfg.ToolLoopMax)
	}
	if cfg.ModelBackend != "native-openrouter" {
		t.Fatalf("expected default model backend native-openrouter, got %q", cfg.ModelBackend)
	}
	if cfg.BifrostBaseURL != "" {
		t.Fatalf("expected empty default Bifrost base URL, got %q", cfg.BifrostBaseURL)
	}
	if cfg.AgentGatewayBaseURL != "" {
		t.Fatalf("expected empty default agentgateway base URL, got %q", cfg.AgentGatewayBaseURL)
	}
	if cfg.AgentGatewayAPIKey != "" {
		t.Fatalf("expected empty default agentgateway API key, got %q", cfg.AgentGatewayAPIKey)
	}
	if cfg.AgentGatewayReadinessURL != "" {
		t.Fatalf("expected empty default agentgateway readiness URL, got %q", cfg.AgentGatewayReadinessURL)
	}
	if cfg.EnableServerContextResolver {
		t.Fatalf("expected server context resolver disabled by default")
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
	t.Setenv("AI_ORCH_POLICY_ENGINE", "agt")
	t.Setenv("AI_ORCH_CONSECUTIVE_TOOL_CALL_MAX", "7")
	t.Setenv("AI_ORCH_MODEL_BACKEND", "bifrost")
	t.Setenv("AI_ORCH_BIFROST_BASE_URL", "http://bifrost:8080")
	t.Setenv("AI_ORCH_BIFROST_API_KEY", "env-bifrost-token")
	t.Setenv("AI_ORCH_AGENTGATEWAY_BASE_URL", "http://agentgateway:3000")
	t.Setenv("AI_ORCH_AGENTGATEWAY_API_KEY", "env-agentgateway-token")
	t.Setenv("AI_ORCH_AGENTGATEWAY_READINESS_URL", "http://agentgateway:15021/healthz/ready")
	t.Setenv("AI_ORCH_ENABLE_SERVER_CONTEXT_RESOLVER", "true")

	cfg, err := Load([]string{"-addr", ":7777", "-audit-path", "/tmp/flag-audit.jsonl", "-dev-token", "flag-token", "-service-token", "flag-service-token", "-classification-max", "restricted", "-kill-switch=false", "-cost-cap-enabled=false", "-session-cost-cap-usd", "0.75", "-policy-engine", "native", "-consecutive-tool-call-max", "11", "-model-backend", "native-openrouter", "-bifrost-base-url", "http://flag-bifrost:8080", "-bifrost-api-key", "flag-bifrost-token", "-agentgateway-base-url", "http://flag-agentgateway:3000", "-agentgateway-api-key", "flag-agentgateway-token", "-agentgateway-readiness-url", "http://flag-agentgateway:15021/healthz/ready", "-enable-server-context-resolver=false"})
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
	if cfg.PolicyEngine != "native" {
		t.Fatalf("expected flag policy engine to win, got %q", cfg.PolicyEngine)
	}
	if cfg.ToolLoopMax != 11 {
		t.Fatalf("expected flag tool loop max to win, got %d", cfg.ToolLoopMax)
	}
	if cfg.ModelBackend != "native-openrouter" {
		t.Fatalf("expected flag model backend to win, got %q", cfg.ModelBackend)
	}
	if cfg.BifrostBaseURL != "http://flag-bifrost:8080" {
		t.Fatalf("expected flag Bifrost base URL to win, got %q", cfg.BifrostBaseURL)
	}
	if cfg.BifrostAPIKey != "flag-bifrost-token" {
		t.Fatalf("expected flag Bifrost API key to win, got %q", cfg.BifrostAPIKey)
	}
	if cfg.AgentGatewayBaseURL != "http://flag-agentgateway:3000" {
		t.Fatalf("expected flag agentgateway base URL to win, got %q", cfg.AgentGatewayBaseURL)
	}
	if cfg.AgentGatewayAPIKey != "flag-agentgateway-token" {
		t.Fatalf("expected flag agentgateway API key to win, got %q", cfg.AgentGatewayAPIKey)
	}
	if cfg.AgentGatewayReadinessURL != "http://flag-agentgateway:15021/healthz/ready" {
		t.Fatalf("expected flag agentgateway readiness URL to win, got %q", cfg.AgentGatewayReadinessURL)
	}
	if cfg.EnableServerContextResolver {
		t.Fatalf("expected flag server context resolver setting to win")
	}
}
