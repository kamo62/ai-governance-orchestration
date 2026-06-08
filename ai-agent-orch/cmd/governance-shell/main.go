package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ai-agent-orch/internal/appconfig"
	"ai-agent-orch/internal/appversion"
	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/composition"
	"ai-agent-orch/internal/contextresolver"
	"ai-agent-orch/internal/governance"
	"ai-agent-orch/internal/governanceui"
	"ai-agent-orch/internal/httpauth"
	"ai-agent-orch/internal/modelbackend"
	"ai-agent-orch/internal/modelgateway"
	"ai-agent-orch/internal/oauth"
	"ai-agent-orch/internal/openrouter"
	"ai-agent-orch/internal/policyengine"
	"ai-agent-orch/internal/router"
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
	var modelPricingStore governance.ModelPricingStore
	var modelPricingCloser interface{ Close() error }
	if hasSQLiteExt(cfg.AuditPath) {
		store, err := governance.NewSQLiteSessionStore(cfg.AuditPath)
		if err != nil {
			log.Fatalf("session store init failed: %v", err)
		}
		sessionStore = store
		pricingStore, err := governance.NewSQLiteModelPricingStore(cfg.AuditPath)
		if err != nil {
			log.Fatalf("model pricing store init failed: %v", err)
		}
		modelPricingStore = pricingStore
		modelPricingCloser = pricingStore
	}

	var sessionContextResolver governance.ContextResolver
	if cfg.EnableServerContextResolver {
		log.Println("server-side context resolver enabled for local development")
		sessionContextResolver = contextResolverAdapter{resolver: contextresolver.New("")}
	}

	sessionService := governance.NewSessionService(governance.SessionConfig{
		DevToken:           cfg.DevToken,
		AdminToken:         cfg.AdminToken,
		Authorizer:         requestAuthorizer,
		Audit:              auditStore,
		Sessions:           sessionStore,
		ModelPricing:       modelPricingStore,
		ClassificationMax:  cfg.ClassificationMax,
		KillSwitch:         cfg.KillSwitch,
		KillSwitchStore:    killSwitchStore,
		CostCapEnabled:     cfg.CostCapEnabled,
		SessionCostCapUSD:  cfg.SessionCostCapUSD,
		PolicyEngine:       policyEngine,
		ToolLoopMax:        cfg.ToolLoopMax,
		Metrics:            metricsHandler,
		ContextResolver:    sessionContextResolver,
		TrustedClientToken: cfg.TrustedClientToken,
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
	openRouterClient := openrouter.NewClient(openrouter.Config{
		APIKey:   os.Getenv("OPENROUTER_API_KEY"),
		BaseURL:  os.Getenv("OPENROUTER_BASE_URL"),
		Referer:  os.Getenv("OPENROUTER_HTTP_REFERER"),
		AppTitle: envOrDefault("OPENROUTER_APP_TITLE", "ai-agent-orch-local"),
	})
	modelBackend, err := modelbackend.New(modelbackend.BackendConfig{
		Name:                     cfg.ModelBackend,
		OpenRouterClient:         openRouterClient,
		BifrostBaseURL:           cfg.BifrostBaseURL,
		BifrostAPIKey:            cfg.BifrostAPIKey,
		AgentGatewayBaseURL:      cfg.AgentGatewayBaseURL,
		AgentGatewayAPIKey:       cfg.AgentGatewayAPIKey,
		AgentGatewayReadinessURL: cfg.AgentGatewayReadinessURL,
	})
	if err != nil {
		log.Fatalf("model backend init failed: %v", err)
	}
	if healthBackend, ok := modelBackend.(interface{ Health(context.Context) error }); ok {
		healthCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		if err := waitForModelBackendHealth(healthCtx, healthBackend, 1*time.Second); err != nil {
			cancel()
			log.Fatalf("model backend health failed: %v", err)
		}
		cancel()
	}
	log.Printf("model backend selected: %s", modelBackend.Name())

	handler := http.NewServeMux()
	handler.Handle("/ui", governanceui.Redirect())
	handler.Handle("/ui/", http.StripPrefix("/ui/", governanceui.Handler()))
	handler.Handle("/", baseHandler)
	handler.Handle("/v1/system/status", governance.NewSystemStatusHandler(governance.SystemStatusConfig{
		Service:               "governance-shell",
		Version:               appversion.Version,
		ModelBackend:          modelBackend.Name(),
		GatewayAddr:           cfg.GatewayAddr,
		RuntimeGatewayEnabled: cfg.RuntimeToken != "",
		ClassificationMax:     cfg.ClassificationMax,
		PolicyEngine:          cfg.PolicyEngine,
		Gateways: []governance.GatewayOption{
			{ID: "bifrost", Label: "Bifrost", Mode: "sidecar", Default: true},
			{ID: "agentgateway", Label: "AgentGateway", Mode: "sidecar", ComposeFile: "docker-compose.agentgateway.yml"},
			{ID: "native-openrouter", Label: "OpenRouter", Mode: "direct", ComposeFile: "docker-compose.openrouter.yml"},
		},
	}))
	handler.Handle("/v1/agents", governance.NewAgentListHandler(cfg.CatalogRoot))
	handler.Handle("/v1/runs", governance.NewRunHandler(sessionService, orchClient))
	handler.Handle("/v1/sessions", governance.NewSessionHandler(sessionService))
	handler.Handle("/v1/sessions/", &sessionSubrouter{
		sessionService: sessionService,
		orchClient:     orchClient,
		events:         eventStore,
	})
	handler.Handle("/v1/audit/sessions/", governance.NewAuditLookupHandler(governance.AuditLookupConfig{
		DevToken:     cfg.DevToken,
		Authorizer:   requestAuthorizer,
		Audit:        auditStore,
		ModelPricing: modelPricingStore,
	}))
	handler.Handle("/v1/admin/killswitch", governance.NewAdminHandler(killSwitchStore, sessionService))
	handler.Handle("/v1/admin/killswitch/", governance.NewAdminHandler(killSwitchStore, sessionService))
	handler.Handle("/v1/admin/audit/retention", governance.NewAdminAuditHandler(auditStore, sessionService))
	handler.Handle("/v1/compositions", governance.NewCompositionHandler(sessionService, compositionStore))
	handler.Handle("/v1/compositions/", governance.NewCompositionHandler(sessionService, compositionStore))
	registerRegistryHandlers(handler, governance.NewRegistryHandlerWithMetrics(registryStore, sessionService, metricsHandler))
	if err := governance.SeedPOCRegistryDefaults(registryStore); err != nil {
		log.Printf("registry seed warning: %v", err)
	}
	mcpProxy := governance.NewMCPProxyHandler(governance.MCPProxyConfig{
		ServiceToken:      cfg.ServiceToken,
		DevToken:          cfg.DevToken,
		Audit:             auditStore,
		Sessions:          sessionStore,
		Registrations:     defaultMCPRegistrations(cfg.CatalogRoot, cfg.ClassificationMax),
		UserTokens:        governance.NewOAuthTokenStoreAdapter(oauthTokenStore),
		PolicyEngine:      policyEngine,
		ClassificationMax: cfg.ClassificationMax,
	})
	handler.Handle("/v1/mcp/", mcpProxy)
	handler.Handle("/internal/v1/model/", governance.NewModelProxyHandler(governance.ModelProxyConfig{
		ServiceToken: cfg.ServiceToken,
		Backend:      modelBackend,
		Audit:        auditStore,
	}))
	handler.Handle("/internal/v1/mcp/", mcpProxy)
	handler.Handle("/metrics", metricsHandler)

	// Centralized auth middleware with explicit public allow-list.
	// Internal proxy endpoints use service-token auth inside their own handlers.
	publicPaths := []string{"/ui", "/mcp/healthz", "/readyz", "/healthz", "/metrics", "/internal/v1/model/", "/internal/v1/mcp/", "/v1/mcp/"}
	authHandler := governance.AuthMiddleware(sessionService, publicPaths)(handler)

	wrappedHandler := logRequestLatency(authHandler)
	// TLS note: ListenAndServe runs plain HTTP. In production, terminate TLS at
	// a reverse proxy (nginx, Envoy, cloud LB) and forward to this port. If
	// running directly on the internet, replace ListenAndServe with
	// ListenAndServeTLS or use autocert.
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           wrappedHandler,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		// WriteTimeout is 0 because SSE streams need indefinite write time.
		// Per-request deadlines are enforced by handlers where appropriate.
		WriteTimeout:   0,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1 MiB
	}

	// Model Compatibility Gateway (Phase 1G + 1J).
	var gatewaySrv *http.Server
	if cfg.RuntimeToken != "" {
		if sessionStore == nil {
			log.Fatal("model compatibility gateway requires durable session storage; configure a SQLite audit path")
		}
		modelRegistry, err := catalog.LoadModelRegistry(cfg.CatalogRoot)
		if err != nil {
			log.Printf("model registry load failed: %v", err)
		} else {
			govRouter := router.New(modelRegistry)
			gateway := modelgateway.NewGateway(modelgateway.GatewayConfig{
				RuntimeToken: cfg.RuntimeToken,
				Router:       govRouter,
				Backend:      modelBackend,
				Audit:        auditStore,
				LookupSession: func(ctx context.Context, sessionID string) (modelgateway.SessionInfo, error) {
					record, err := sessionService.SessionRecord(ctx, sessionID)
					if err != nil {
						return modelgateway.SessionInfo{}, err
					}
					return modelgateway.SessionInfo{Classification: record.Classification}, nil
				},
			})
			gatewaySrv = &http.Server{
				Addr:              cfg.GatewayAddr,
				Handler:           gateway.Handler(),
				ReadTimeout:       15 * time.Second,
				ReadHeaderTimeout: 5 * time.Second,
				WriteTimeout:      0, // SSE streams need indefinite write time
				IdleTimeout:       120 * time.Second,
				MaxHeaderBytes:    1 << 20,
			}
			go func() {
				log.Printf("model compatibility gateway listening on %s", cfg.GatewayAddr)
				if err := gatewaySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Fatalf("gateway error: %v", err)
				}
			}()
		}
	} else {
		log.Println("model compatibility gateway disabled: no runtime token configured")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if modelPricingStore != nil {
		governance.StartModelPricingRefresh(ctx, modelPricingStore, governance.OpenRouterPricingFetcher{
			BaseURL: os.Getenv("OPENROUTER_BASE_URL"),
		}, envDurationOrDefault("AI_ORCH_MODEL_PRICING_REFRESH_INTERVAL", 24*time.Hour), log.Printf)
	}

	go func() {
		log.Printf("governance-shell listening on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}
	if gatewaySrv != nil {
		if err := gatewaySrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("gateway shutdown error: %v", err)
		}
	}
	if modelPricingCloser != nil {
		if err := modelPricingCloser.Close(); err != nil {
			log.Printf("model pricing store close error: %v", err)
		}
	}
	log.Println("shutdown complete")
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

type contextResolverAdapter struct {
	resolver *contextresolver.Resolver
}

func (a contextResolverAdapter) Resolve() governance.SessionContext {
	if a.resolver == nil {
		return governance.SessionContext{}
	}
	resolved := a.resolver.Resolve()
	return governance.SessionContext{
		RepoURL:      resolved.RepoURL,
		Branch:       resolved.Branch,
		CommitSHA:    resolved.CommitSHA,
		WorkItemID:   resolved.WorkItemID,
		WorkItemType: resolved.WorkItemType,
		ActorHint:    resolved.ActorHint,
		SourceSystem: resolved.SourceSystem,
	}
}

type modelBackendHealth interface {
	Health(context.Context) error
}

func waitForModelBackendHealth(ctx context.Context, backend modelBackendHealth, interval time.Duration) error {
	if backend == nil {
		return nil
	}
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	var lastErr error
	for {
		if err := backend.Health(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("backend did not become healthy before timeout: %w; last error: %v", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
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
	case containsSuffix(path, "/turns"):
		governance.NewTurnsHandler(sr.sessionService, sr.orchClient, sr.events).ServeHTTP(w, r)
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

func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		log.Printf("invalid %s=%q; using %s", key, value, fallback)
		return fallback
	}
	return duration
}

func defaultMCPRegistrations(catalogRoot string, classificationMax string) map[string]governance.MCPProxyRegistration {
	platformToken := os.Getenv("AI_ORCH_MCP_TOKEN")
	registrations, err := catalog.LoadMCPRegistrations(catalogRoot)
	if err != nil {
		log.Fatalf("load mcp registrations: %v", err)
	}
	endpoints := map[string]string{
		"repo-classification":      "http://mcp-repo-classification:8091",
		"engineering-standards-kb": "http://mcp-engineering-standards-kb:8092",
		"catalog-introspection":    "http://mcp-catalog-introspection:8093",
		"playwright-cli":           "http://mcp-playwright-cli:8094",
		"issue-tracker":            "http://mcp-issue-tracker:8095",
		"documentation":            "http://mcp-documentation:8096",
		"test-management":          "http://mcp-test-management:8097",
	}
	runtime := make(map[string]governance.MCPProxyRegistration, len(registrations))
	for serverID, reg := range registrations {
		endpoint := reg.Endpoint
		if override := endpoints[serverID]; override != "" {
			endpoint = override
		}
		runtime[serverID] = governance.MCPProxyRegistration{
			Endpoint:          endpoint,
			AuthMode:          reg.AuthMode,
			PlatformToken:     platformToken,
			AllowedAgents:     reg.AllowedAgents,
			ToolAllow:         reg.ToolPolicy.Allow,
			ToolDeny:          reg.ToolPolicy.Deny,
			ClassificationMax: classificationMax,
		}
	}
	return runtime
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
