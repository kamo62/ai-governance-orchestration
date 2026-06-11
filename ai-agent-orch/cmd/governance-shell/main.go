package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"ai-agent-orch/internal/copilot"
	"ai-agent-orch/internal/governance"
	"ai-agent-orch/internal/governanceui"
	"ai-agent-orch/internal/httpauth"
	"ai-agent-orch/internal/modelbackend"
	"ai-agent-orch/internal/modelgateway"
	"ai-agent-orch/internal/oauth"
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
	var killSwitchStore governance.KillSwitchStore
	var killSwitchCloser interface{ Close() error }
	if hasSQLiteExt(cfg.AuditPath) {
		durableKillSwitch, err := governance.NewSQLiteKillSwitch(cfg.AuditPath)
		if err != nil {
			log.Fatalf("kill switch store init failed: %v", err)
		}
		killSwitchStore = durableKillSwitch
		killSwitchCloser = durableKillSwitch
	} else {
		killSwitchStore = governance.NewMemoryKillSwitch()
		log.Println("kill switch store is in-memory; state resets on restart")
	}
	metricsHandler := governance.NewMetricsHandler()
	policyEngine, err := policyengine.New(cfg.PolicyEngine)
	if err != nil {
		log.Fatalf("policy engine init failed: %v", err)
	}

	// OIDC validator: when OIDC_ISSUER_URL and OIDC_CLIENT_ID are set,
	// Bearer tokens are treated as OIDC id_tokens. Otherwise the
	// existing dev-token check remains the only gate. In production the
	// shared dev token is not accepted alongside OIDC: identity must come
	// from verified token claims, never from a static secret.
	oidcDevToken := cfg.DevToken
	if cfg.IsProduction() {
		oidcDevToken = ""
	}
	oidcValidator := httpauth.NewOIDCTokenValidator(httpauth.OIDCConfig{
		IssuerURL: os.Getenv("OIDC_ISSUER_URL"),
		ClientID:  os.Getenv("OIDC_CLIENT_ID"),
	}, oidcDevToken)
	var requestAuthorizer governance.RequestAuthorizer
	if oidcValidator.IsOIDCEnabled() {
		log.Println("OIDC auth enabled")
		requestAuthorizer = oidcValidator
	}

	// Production posture fails closed: shared dev tokens, local default
	// secrets, and header-asserted identity are local-dev conveniences only.
	if cfg.IsProduction() {
		problems := cfg.ValidateProduction()
		if requestAuthorizer == nil {
			problems = append(problems, "OIDC_ISSUER_URL and OIDC_CLIENT_ID must be configured in production; dev-token auth with header-asserted identity is not allowed")
		}
		if len(problems) > 0 {
			for _, problem := range problems {
				log.Printf("production config error: %s", problem)
			}
			log.Fatal("refusing to start with AI_ORCH_ENV=production and unsafe configuration")
		}
		log.Println("production posture active")
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
		RequireWorkItem:    cfg.RequireWorkItem,
		TrustedClientToken: cfg.TrustedClientToken,
	})
	eventStore := governance.NewEventStore()
	compositionStore := composition.NewCompositionStore()

	// MCP oauth-user tokens persist encrypted when durable storage and an
	// encryption key are available; otherwise grants reset on restart.
	var oauthTokenStore oauth.TokenStore
	var oauthTokenCloser interface{ Close() error }
	oauthKey := envOrDefault("AI_ORCH_OAUTH_TOKEN_ENCRYPTION_KEY", os.Getenv("AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY"))
	if hasSQLiteExt(cfg.AuditPath) && oauthKey != "" {
		durableTokens, err := oauth.NewSQLiteTokenStore(cfg.AuditPath, oauthKey)
		if err != nil {
			log.Fatalf("oauth token store init failed: %v", err)
		}
		oauthTokenStore = durableTokens
		oauthTokenCloser = durableTokens
	} else {
		oauthTokenStore = oauth.NewMemoryTokenStore()
		log.Println("oauth token store is in-memory; MCP user grants reset on restart")
	}
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
	var copilotStore *copilot.Store
	var copilotResolver modelbackend.CopilotTokenResolver
	if os.Getenv("AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY") != "" {
		store, err := copilot.OpenStore(os.Getenv("AI_ORCH_COPILOT_TOKEN_DB"), os.Getenv("AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY"))
		if err != nil {
			log.Fatalf("copilot token store init failed: %v", err)
		}
		copilotStore = store
		copilotResolver = copilotStoreResolver{store: store, client: copilot.NewClient()}
		if count, err := store.EnrollmentCount(context.Background()); err == nil {
			log.Printf("copilot token store opened: %d enrollment(s)", count)
		}
	}
	modelBackend, err := modelbackend.New(modelbackend.BackendConfig{
		Name:                 cfg.ModelBackend,
		BifrostBaseURL:       cfg.BifrostBaseURL,
		BifrostAPIKey:        cfg.BifrostAPIKey,
		CopilotTokenResolver: copilotResolver,
	})
	if err != nil {
		log.Fatalf("model backend init failed: %v", err)
	}
	if modelBackend.Name() != modelbackend.BackendCopilotUser && copilotResolver != nil {
		copilotBackend := modelbackend.NewCopilotUserBackend(copilot.NewClient(), copilotResolver)
		modelBackend = modelbackend.NewRoutedBackend(modelBackend, map[string]modelbackend.Backend{
			modelbackend.BackendCopilotUser: copilotBackend,
		})
	}
	if healthBackend, ok := modelBackend.(interface{ Health(context.Context) error }); ok {
		healthCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		if err := waitForModelBackendHealth(healthCtx, healthBackend, 1*time.Second); err != nil {
			cancel()
			if cfg.RequireBackendHealth {
				log.Fatalf("model backend health failed: %v", err)
			}
			// Keep serving so governance APIs, audit, and the UI stay up;
			// model calls will fail per-request until the backend recovers.
			log.Printf("model backend unhealthy at startup, continuing degraded: %v", err)
		} else {
			cancel()
		}
	}
	log.Printf("model backend selected: %s", modelBackend.Name())
	pricingFetcher := governance.OpenRouterPricingFetcher{BaseURL: os.Getenv("OPENROUTER_BASE_URL")}
	pricingBootstrapped := false
	if modelPricingStore != nil {
		pricingBootstrapped = bootstrapModelPricing(context.Background(), modelPricingStore, pricingFetcher)
	}

	handler := http.NewServeMux()
	handler.Handle("/ui", governanceui.Redirect())
	handler.Handle("/ui/", http.StripPrefix("/ui/", governanceui.Handler()))
	handler.Handle("/", baseHandler)
	gatewayOptions := []governance.GatewayOption{
		{ID: "bifrost", Label: "Bifrost", Mode: "sidecar", Default: true},
		{ID: "copilot-user", Label: "GitHub Copilot", Mode: "per-user", ComposeFile: "docker-compose.copilot.yml"},
	}
	handler.Handle("/v1/system/status", governance.NewSystemStatusHandler(governance.SystemStatusConfig{
		Service:               "governance-shell",
		Version:               appversion.Version,
		Environment:           cfg.Environment,
		ModelBackend:          modelBackend.Name(),
		GatewayAddr:           cfg.GatewayAddr,
		RuntimeGatewayEnabled: cfg.RuntimeToken != "",
		ClassificationMax:     cfg.ClassificationMax,
		PolicyEngine:          cfg.PolicyEngine,
		Gateways:              gatewayOptions,
	}))
	handler.Handle("/v1/backends", governance.NewBackendHandler(governance.BackendHandlerConfig{
		CurrentBackend: modelBackend.Name(),
		GatewayOptions: gatewayOptions,
		AdminToken:     cfg.AdminToken,
		ControlEnabled: cfg.BackendControlEnabled,
		WorkDir:        cfg.BackendControlWorkDir,
	}))
	if copilotStore != nil {
		handler.Handle("/v1/copilot/", governance.NewCopilotHandler(governance.CopilotHandlerConfig{DevToken: cfg.DevToken, Authorizer: requestAuthorizer, Store: copilotStore}))
	}
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
		Sessions:     sessionStore,
	}))
	handler.Handle("/v1/admin/killswitch", governance.NewAdminHandler(killSwitchStore, sessionService))
	handler.Handle("/v1/admin/killswitch/", governance.NewAdminHandler(killSwitchStore, sessionService))
	handler.Handle("/v1/admin/audit/retention", governance.NewAdminAuditHandler(auditStore, sessionService))
	handler.Handle("/v1/admin/sessions", governance.NewAdminSessionsHandler(sessionService))
	handler.Handle("/v1/admin/sessions/export", governance.NewAdminSessionsExportHandler(sessionService))
	handler.Handle("/v1/admin/audit/sessions/", governance.NewAdminAuditLookupHandler(governance.AdminAuditLookupConfig{
		Service:      sessionService,
		Audit:        auditStore.(governance.AuditReader),
		ModelPricing: modelPricingStore,
	}))
	adminRegistryHandler := governance.NewAdminRegistryHandler(registryStore, sessionService)
	handler.Handle("/v1/admin/evidence", adminRegistryHandler)
	handler.Handle("/v1/admin/cache-outcomes", adminRegistryHandler)
	handler.Handle("/v1/admin/reporting/maturity-governance", adminRegistryHandler)
	handler.Handle("/v1/compositions", governance.NewCompositionHandler(sessionService, compositionStore))
	handler.Handle("/v1/compositions/", governance.NewCompositionHandler(sessionService, compositionStore))
	registerRegistryHandlers(handler, governance.NewRegistryHandlerWithMetrics(registryStore, sessionService, metricsHandler))
	if err := governance.SeedPOCRegistryDefaults(registryStore); err != nil {
		log.Printf("registry seed warning: %v", err)
	}
	mcpProxy := governance.NewMCPProxyHandler(governance.MCPProxyConfig{
		ServiceToken:      cfg.ServiceToken,
		DevToken:          cfg.DevToken,
		Authorizer:        requestAuthorizer,
		Audit:             auditStore,
		Sessions:          sessionStore,
		Registrations:     defaultMCPRegistrations(cfg.CatalogRoot, cfg.ClassificationMax),
		UserTokens:        governance.NewOAuthTokenStoreAdapter(oauthTokenStore),
		PolicyEngine:      policyEngine,
		ClassificationMax: cfg.ClassificationMax,
	})
	handler.Handle("/v1/mcp/", mcpProxy)
	handler.Handle("/internal/v1/model/", governance.NewModelProxyHandler(governance.ModelProxyConfig{
		ServiceToken:  cfg.ServiceToken,
		Backend:       modelBackend,
		Audit:         auditStore,
		LookupSession: sessionService.SessionRecord,
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
			govRouter := router.NewWithRouteAvailability(modelRegistry, func(ctx context.Context, route catalog.ModelRoute, req router.Request) bool {
				if strings.TrimSpace(route.Provider) != modelbackend.BackendCopilotUser {
					return true
				}
				if copilotResolver == nil || strings.TrimSpace(req.ActorSubject) == "" {
					return false
				}
				_, err := copilotResolver.TokenForActor(ctx, req.ActorSubject)
				return err == nil
			})
			gatewayConfig := modelgateway.GatewayConfig{
				RuntimeToken:    cfg.RuntimeToken,
				Router:          govRouter,
				Backend:         modelBackend,
				Audit:           auditStore,
				MaxRequestBytes: int64(cfg.GatewayMaxRequestBytes),
				LookupSession: func(ctx context.Context, sessionID string) (modelgateway.SessionInfo, error) {
					record, err := sessionService.SessionRecord(ctx, sessionID)
					if err != nil {
						return modelgateway.SessionInfo{}, err
					}
					return modelgateway.SessionInfo{
						SessionID:                 record.SessionID,
						Classification:            record.Classification,
						Status:                    record.Status,
						GatewayTokenSHA256:        record.GatewayTokenSHA256,
						RuntimeGatewayTokenSHA256: record.RuntimeGatewayTokenSHA256,
						ActorSubject:              record.ActorSubject,
						Agent:                     record.Agent,
						RunID:                     record.RunID,
						UseCaseID:                 record.UseCaseID,
						WorkflowID:                record.WorkflowID,
						WorkItemID:                record.WorkItemID,
						WorkItemType:              record.WorkItemType,
						RepoURL:                   record.RepoURL,
						Branch:                    record.Branch,
						CommitSHA:                 record.CommitSHA,
						ActorHint:                 record.ActorHint,
						SourceSystem:              record.SourceSystem,
						PermissionMode:            record.PermissionMode,
						ApprovalMode:              record.ApprovalMode,
						WorkspaceMode:             record.WorkspaceMode,
					}, nil
				},
			}
			if cfg.GatewayAutoSession {
				gatewayConfig.AutoSession = func(ctx context.Context, request modelgateway.AutoSessionRequest) (modelgateway.SessionInfo, error) {
					result, err := sessionService.CreateAutoGatewaySession(ctx, governance.AutoGatewaySessionRequest{
						ActorSubject:       request.ActorSubject,
						Classification:     request.Classification,
						PromptSHA256:       request.PromptSHA256,
						ModelAlias:         request.ModelAlias,
						Client:             request.Client,
						Endpoint:           request.Endpoint,
						RawRequestBody:     request.RawRequestBody,
						TrustedClientToken: request.TrustedClientToken,
						UseCaseID:          request.UseCaseID,
						WorkflowID:         request.WorkflowID,
						WorkItemID:         request.WorkItemID,
						WorkItemType:       request.WorkItemType,
						RepoURL:            request.RepoURL,
						Branch:             request.Branch,
						CommitSHA:          request.CommitSHA,
						Intent:             request.Intent,
						ActorHint:          request.ActorHint,
						SourceSystem:       request.SourceSystem,
						EstimatedCostUSD:   request.EstimatedCostUSD,
					})
					if err != nil {
						return modelgateway.SessionInfo{}, err
					}
					record := result.Record
					return modelgateway.SessionInfo{
						SessionID:          record.SessionID,
						Classification:     record.Classification,
						Status:             record.Status,
						GatewayTokenSHA256: record.GatewayTokenSHA256,
						ActorSubject:       record.ActorSubject,
						Agent:              record.Agent,
						UseCaseID:          record.UseCaseID,
						WorkflowID:         record.WorkflowID,
						WorkItemID:         record.WorkItemID,
						WorkItemType:       record.WorkItemType,
						RepoURL:            record.RepoURL,
						Branch:             record.Branch,
						CommitSHA:          record.CommitSHA,
						ActorHint:          record.ActorHint,
						SourceSystem:       record.SourceSystem,
						PermissionMode:     record.PermissionMode,
						ApprovalMode:       record.ApprovalMode,
						WorkspaceMode:      record.WorkspaceMode,
						GatewayToken:       result.GatewayToken,
					}, nil
				}
			}
			gateway := modelgateway.NewGateway(gatewayConfig)
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
		governance.StartModelPricingRefresh(ctx, modelPricingStore, pricingFetcher, envDurationOrDefault("AI_ORCH_MODEL_PRICING_REFRESH_INTERVAL", 24*time.Hour), !pricingBootstrapped, log.Printf)
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
	if copilotStore != nil {
		if err := copilotStore.Close(); err != nil {
			log.Printf("copilot token store close error: %v", err)
		}
	}
	if killSwitchCloser != nil {
		if err := killSwitchCloser.Close(); err != nil {
			log.Printf("kill switch store close error: %v", err)
		}
	}
	if oauthTokenCloser != nil {
		if err := oauthTokenCloser.Close(); err != nil {
			log.Printf("oauth token store close error: %v", err)
		}
	}
	log.Println("shutdown complete")
}

type copilotStoreResolver struct {
	store  *copilot.Store
	client *copilot.Client
}

func (r copilotStoreResolver) TokenForActor(ctx context.Context, actorSubject string) (copilot.TokenRecord, error) {
	rec, err := r.store.Load(ctx, actorSubject)
	if err != nil {
		return copilot.TokenRecord{}, err
	}
	if rec.RefreshToken == "" || rec.AccessExpiresAt.IsZero() || time.Now().UTC().Before(rec.AccessExpiresAt.Add(-5*time.Minute)) {
		return rec, nil
	}
	client := r.client
	if client == nil {
		client = copilot.NewClient()
	}
	refreshed, err := client.RefreshAccessToken(ctx, rec.RefreshToken)
	if err != nil {
		return rec, nil
	}
	updated, err := r.store.UpdateOAuthToken(ctx, actorSubject, refreshed, time.Now().UTC())
	if err != nil {
		return rec, nil
	}
	return updated, nil
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
		governance.NewEventsHandler(sr.events, sr.sessionService).ServeHTTP(w, r)
	case containsSuffix(path, "/abort"):
		governance.NewAbortHandler(sr.sessionService, sr.events).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

// logRequestLatency wraps an http.Handler, tags every request with an ID, and
// logs requests that exceed a threshold. The ID is echoed in the X-Request-ID
// response header so a slow or failing call can be joined to client logs.
func logRequestLatency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		start := time.Now()
		next.ServeHTTP(w, r)
		dur := time.Since(start)
		if dur > 500*time.Millisecond {
			log.Printf("slow request: %s %s %s request_id=%s", r.Method, r.URL.Path, dur, requestID)
		}
	})
}

func newRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return "req_" + hex.EncodeToString(b[:])
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

func bootstrapModelPricing(ctx context.Context, store governance.ModelPricingStore, fetcher governance.OpenRouterPricingFetcher) bool {
	if store == nil {
		return false
	}
	timeout := envDurationOrDefault("AI_ORCH_MODEL_PRICING_BOOTSTRAP_TIMEOUT", 15*time.Second)
	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	count, err := governance.RefreshModelPricing(refreshCtx, store, fetcher)
	if err != nil {
		log.Printf("model pricing bootstrap failed: %v", err)
		return false
	}
	log.Printf("model pricing bootstrapped: %d models", count)
	return true
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
