package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"ai-agent-orch/internal/appconfig"
	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/composition"
	"ai-agent-orch/internal/governance"
	"ai-agent-orch/internal/httpauth"
	"ai-agent-orch/internal/oauth"
	"ai-agent-orch/internal/openrouter"
	"ai-agent-orch/internal/policyengine"
	"ai-agent-orch/internal/server"
)

func main() {
	cfg, err := appconfig.Load(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	baseHandler := server.New("governance-shell", func() error {
		_, err := catalog.Validate(cfg.CatalogRoot)
		return err
	}, func() (server.CatalogSummary, error) {
		report, err := catalog.Validate(cfg.CatalogRoot)
		if err != nil {
			return server.CatalogSummary{}, err
		}
		return server.CatalogSummary{Agents: len(report.Agents), Models: len(report.ModelAliases)}, nil
	})

	auditStore, err := newAuditStore(cfg.AuditPath)
	if err != nil {
		log.Fatal(err)
	}
	auditStore = audit.NewChainAppender(auditStore)
	killSwitchStore := governance.NewMemoryKillSwitch()
	metricsHandler := governance.NewMetricsHandler()
	policyEngine, err := policyengine.New(cfg.PolicyEngine)
	if err != nil {
		log.Fatalf("policy engine init failed: %v", err)
	}

	// OIDC validator: when OIDC_ISSUER_URL and OIDC_CLIENT_ID are set,
	// Bearer tokens are treated as OIDC id_tokens. Otherwise the
	// existing dev-token check remains the only gate.
	oidcValidator := httpauth.NewOIDCTokenValidator(httpauth.OIDCConfig{
		IssuerURL: os.Getenv("OIDC_ISSUER_URL"),
		ClientID:  os.Getenv("OIDC_CLIENT_ID"),
	}, cfg.DevToken)
	var requestAuthorizer governance.RequestAuthorizer
	if oidcValidator.IsOIDCEnabled() {
		log.Println("OIDC auth enabled")
		requestAuthorizer = oidcValidator
	}

	// Initialize session store (SQLite-backed when audit path is SQLite).
	var sessionStore governance.SessionStore
	if hasSQLiteExt(cfg.AuditPath) {
		store, err := governance.NewSQLiteSessionStore(cfg.AuditPath)
		if err != nil {
			log.Fatalf("session store init failed: %v", err)
		}
		sessionStore = store
	}

	sessionService := governance.NewSessionService(governance.SessionConfig{
		DevToken:          cfg.DevToken,
		Authorizer:        requestAuthorizer,
		Audit:             auditStore,
		Sessions:          sessionStore,
		ClassificationMax: cfg.ClassificationMax,
		KillSwitch:        cfg.KillSwitch,
		KillSwitchStore:   killSwitchStore,
		CostCapEnabled:    cfg.CostCapEnabled,
		SessionCostCapUSD: cfg.SessionCostCapUSD,
		PolicyEngine:      policyEngine,
		ToolLoopMax:       cfg.ToolLoopMax,
		Metrics:           metricsHandler,
	})
	eventStore := governance.NewEventStore()
	compositionStore := composition.NewCompositionStore()
	oauthTokenStore := oauth.NewMemoryTokenStore()
	var registryStore governance.RegistryStoreInterface
	if hasSQLiteExt(cfg.AuditPath) {
		durableStore, err := governance.NewDurableRegistryStore(cfg.AuditPath)
		if err != nil {
			log.Fatalf("durable registry store init failed for %s: %v", cfg.AuditPath, err)
		} else {
			log.Printf("using durable registry store: %s", cfg.AuditPath)
			registryStore = durableStore
		}
	} else {
		registryStore = governance.NewRegistryStore()
	}

	orchestratorURL := os.Getenv("AI_ORCH_ORCHESTRATOR_URL")
	if orchestratorURL == "" {
		orchestratorURL = "http://127.0.0.1:8081"
	}
	orchClient := governance.NewOrchestratorHTTPClient(orchestratorURL, cfg.ServiceToken)

	handler := http.NewServeMux()
	handler.Handle("/", baseHandler)
	handler.Handle("/v1/agents", governance.NewAgentListHandler(cfg.CatalogRoot))
	handler.Handle("/v1/sessions", governance.NewSessionHandler(sessionService))
	handler.Handle("/v1/sessions/", &sessionSubrouter{
		sessionService: sessionService,
		orchClient:     orchClient,
		events:         eventStore,
	})
	handler.Handle("/v1/audit/sessions/", governance.NewAuditLookupHandler(governance.AuditLookupConfig{
		DevToken:   cfg.DevToken,
		Authorizer: requestAuthorizer,
		Audit:      auditStore,
	}))
	handler.Handle("/v1/admin/killswitch", governance.NewAdminHandler(killSwitchStore, sessionService))
	handler.Handle("/v1/admin/killswitch/", governance.NewAdminHandler(killSwitchStore, sessionService))
	handler.Handle("/v1/admin/audit/retention", governance.NewAdminAuditHandler(auditStore, sessionService))
	handler.Handle("/v1/compositions", governance.NewCompositionHandler(sessionService, compositionStore))
	handler.Handle("/v1/compositions/", governance.NewCompositionHandler(sessionService, compositionStore))
	registerRegistryHandlers(handler, governance.NewRegistryHandlerWithMetrics(registryStore, sessionService, metricsHandler))
	handler.Handle("/internal/v1/model/", governance.NewModelProxyHandler(governance.ModelProxyConfig{
		ServiceToken: cfg.ServiceToken,
		OpenRouter: openrouter.NewClient(openrouter.Config{
			APIKey:   os.Getenv("OPENROUTER_API_KEY"),
			BaseURL:  os.Getenv("OPENROUTER_BASE_URL"),
			Referer:  os.Getenv("OPENROUTER_HTTP_REFERER"),
			AppTitle: envOrDefault("OPENROUTER_APP_TITLE", "ai-agent-orch-local"),
		}),
		Audit: auditStore,
	}))
	handler.Handle("/internal/v1/mcp/", governance.NewMCPProxyHandler(governance.MCPProxyConfig{
		ServiceToken:  cfg.ServiceToken,
		Audit:         auditStore,
		Registrations: defaultMCPRegistrations(),
		UserTokens:    governance.NewOAuthTokenStoreAdapter(oauthTokenStore),
	}))
	handler.Handle("/metrics", metricsHandler)

	wrappedHandler := logRequestLatency(handler)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           wrappedHandler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
	log.Printf("governance-shell listening on %s", cfg.Addr)
	log.Fatal(srv.ListenAndServe())
}

func registerRegistryHandlers(mux *http.ServeMux, registryHandler http.Handler) {
	mux.Handle("/v1/use-cases", registryHandler)
	mux.Handle("/v1/use-cases/", registryHandler)
	mux.Handle("/v1/workflows", registryHandler)
	mux.Handle("/v1/context-manifests", registryHandler)
	mux.Handle("/v1/context-manifests/", registryHandler)
	mux.Handle("/v1/reporting/maturity-governance", registryHandler)
	mux.Handle("/v1/cache-outcomes", registryHandler)
	mux.Handle("/v1/evidence", registryHandler)
}

// sessionSubrouter dispatches /v1/sessions/{id}/messages, /confirm, /patch-decision, /events.
type sessionSubrouter struct {
	sessionService *governance.SessionService
	orchClient     governance.OrchestratorClient
	events         *governance.EventStore
}

func (sr *sessionSubrouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authReq, ok := sr.sessionService.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	path := r.URL.Path
	switch {
	case containsSuffix(path, "/messages"):
		governance.NewMessagesHandler(sr.sessionService, sr.orchClient).ServeHTTP(w, r)
	case containsSuffix(path, "/confirm"):
		governance.NewConfirmHandlerWithEvents(sr.sessionService, sr.orchClient, sr.events).ServeHTTP(w, r)
	case containsSuffix(path, "/patch-decision"):
		governance.NewPatchDecisionHandler(sr.sessionService).ServeHTTP(w, r)
	case strings.Contains(path, "/patches/"):
		governance.NewPatchFetchHandler(sr.sessionService).ServeHTTP(w, r)
	case containsSuffix(path, "/events"):
		governance.NewEventsHandler(sr.events).ServeHTTP(w, r)
	case containsSuffix(path, "/abort"):
		governance.NewAbortHandler(sr.sessionService, sr.events).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

// logRequestLatency wraps an http.Handler and logs requests that exceed a threshold.
func logRequestLatency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		dur := time.Since(start)
		if dur > 500*time.Millisecond {
			log.Printf("slow request: %s %s %s", r.Method, r.URL.Path, dur)
		}
	})
}

func containsSuffix(path, suffix string) bool {
	return len(path) > len(suffix) && path[len(path)-len(suffix):] == suffix
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func defaultMCPRegistrations() map[string]governance.MCPProxyRegistration {
	platformToken := os.Getenv("AI_ORCH_MCP_TOKEN")
	return map[string]governance.MCPProxyRegistration{
		"repo-classification":      {Endpoint: "http://mcp-repo-classification:8091", AuthMode: "platform", PlatformToken: platformToken},
		"engineering-standards-kb": {Endpoint: "http://mcp-engineering-standards-kb:8092", AuthMode: "platform", PlatformToken: platformToken},
		"catalog-introspection":    {Endpoint: "http://mcp-catalog-introspection:8093", AuthMode: "platform", PlatformToken: platformToken},
		"playwright-cli":           {Endpoint: "http://mcp-playwright-cli:8094", AuthMode: "platform", PlatformToken: platformToken},
		"issue-tracker":            {Endpoint: "http://mcp-issue-tracker:8095", AuthMode: "oauth-user"},
		"documentation":            {Endpoint: "http://mcp-documentation:8096", AuthMode: "oauth-user"},
		"test-management":          {Endpoint: "http://mcp-test-management:8097", AuthMode: "oauth-user"},
	}
}

func newAuditStore(auditPath string) (audit.Store, error) {
	store, err := audit.NewStore(auditPath)
	if err != nil {
		return nil, err
	}
	if hasSQLiteExt(auditPath) {
		log.Printf("using sqlite audit store: %s", auditPath)
	}
	return store, nil
}

func hasSQLiteExt(path string) bool {
	return audit.IsSQLitePath(path)
}
