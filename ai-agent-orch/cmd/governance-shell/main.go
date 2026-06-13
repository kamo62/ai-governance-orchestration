package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/appconfig"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/appversion"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/composition"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/contextresolver"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/copilot"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/envx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/governance"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/governanceui"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpauth"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/logx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/modelbackend"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/modelgateway"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/oauth"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/server"
)

func main() {
	logx.Setup()
	cfg, err := appconfig.Load(os.Args[1:])
	if err != nil {
		logx.Fatal(err)
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
		logx.Fatal(err)
	}
	auditStore = audit.NewChainAppender(auditStore)
	var killSwitchStore governance.KillSwitchStore
	var killSwitchCloser interface{ Close() error }
	if hasSQLiteExt(cfg.AuditPath) {
		durableKillSwitch, err := governance.NewSQLiteKillSwitch(cfg.AuditPath)
		if err != nil {
			logx.Fatalf("kill switch store init failed: %v", err)
		}
		killSwitchStore = durableKillSwitch
		killSwitchCloser = durableKillSwitch
	} else {
		killSwitchStore = governance.NewMemoryKillSwitch()
		logx.Infof("kill switch store is in-memory; state resets on restart")
	}
	metricsHandler := governance.NewMetricsHandler()
	policyEngine, err := policyengine.New(cfg.PolicyEngine)
	if err != nil {
		logx.Fatalf("policy engine init failed: %v", err)
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
		logx.Infof("OIDC auth enabled")
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
				logx.Errorf("production config error: %s", problem)
			}
			logx.Fatal("refusing to start with AI_ORCH_ENV=production and unsafe configuration")
		}
		logx.Infof("production posture active")
	}

	// Initialize session store (SQLite-backed when audit path is SQLite).
	var sessionStore governance.SessionStore
	var modelPricingStore governance.ModelPricingStore
	var modelPricingCloser interface{ Close() error }
	if hasSQLiteExt(cfg.AuditPath) {
		store, err := governance.NewSQLiteSessionStore(cfg.AuditPath)
		if err != nil {
			logx.Fatalf("session store init failed: %v", err)
		}
		sessionStore = store
		pricingStore, err := governance.NewSQLiteModelPricingStore(cfg.AuditPath)
		if err != nil {
			logx.Fatalf("model pricing store init failed: %v", err)
		}
		modelPricingStore = pricingStore
		modelPricingCloser = pricingStore
	}

	var sessionContextResolver governance.ContextResolver
	if cfg.EnableServerContextResolver {
		logx.Infof("server-side context resolver enabled for local development")
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
		ExecutionTimeout:   cfg.ExecutionTimeout,
	})
	eventStore := governance.NewEventStore()
	compositionStore := composition.NewCompositionStore()

	// MCP oauth-user tokens persist encrypted when durable storage and an
	// encryption key are available; otherwise grants reset on restart.
	var oauthTokenStore oauth.TokenStore
	var oauthTokenCloser interface{ Close() error }
	oauthKey := envx.OrDefault("AI_ORCH_OAUTH_TOKEN_ENCRYPTION_KEY", os.Getenv("AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY"))
	if hasSQLiteExt(cfg.AuditPath) && oauthKey != "" {
		durableTokens, err := oauth.NewSQLiteTokenStore(cfg.AuditPath, oauthKey)
		if err != nil {
			logx.Fatalf("oauth token store init failed: %v", err)
		}
		oauthTokenStore = durableTokens
		oauthTokenCloser = durableTokens
	} else {
		oauthTokenStore = oauth.NewMemoryTokenStore()
		logx.Infof("oauth token store is in-memory; MCP user grants reset on restart")
	}
	var registryStore governance.RegistryStoreInterface
	if hasSQLiteExt(cfg.AuditPath) {
		durableStore, err := governance.NewDurableRegistryStore(cfg.AuditPath)
		if err != nil {
			logx.Fatalf("durable registry store init failed for %s: %v", cfg.AuditPath, err)
		} else {
			logx.Infof("using durable registry store: %s", cfg.AuditPath)
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
			logx.Fatalf("copilot token store init failed: %v", err)
		}
		copilotStore = store
		copilotResolver = copilotStoreResolver{store: store, client: copilot.NewClient()}
		if count, err := store.EnrollmentCount(context.Background()); err == nil {
			logx.Infof("copilot token store opened: %d enrollment(s)", count)
		}
	}
	modelBackend, err := modelbackend.New(modelbackend.BackendConfig{
		Name:                 cfg.ModelBackend,
		BifrostBaseURL:       cfg.BifrostBaseURL,
		BifrostAPIKey:        cfg.BifrostAPIKey,
		CopilotTokenResolver: copilotResolver,
	})
	if err != nil {
		logx.Fatalf("model backend init failed: %v", err)
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
				logx.Fatalf("model backend health failed: %v", err)
			}
			// Keep serving so governance APIs, audit, and the UI stay up;
			// model calls will fail per-request until the backend recovers.
			logx.Warnf("model backend unhealthy at startup, continuing degraded: %v", err)
		} else {
			cancel()
		}
	}
	logx.Infof("model backend selected: %s", modelBackend.Name())
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
		logx.Warnf("registry seed warning: %v", err)
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
			logx.Fatal("model compatibility gateway requires durable session storage; configure a SQLite audit path")
		}
		modelRegistry, err := catalog.LoadModelRegistry(cfg.CatalogRoot)
		if err != nil {
			logx.Warnf("model registry load failed: %v", err)
		} else {
			govRouter := router.NewWithRouteAvailability(modelRegistry, func(ctx context.Context, route catalog.ModelRoute, req router.Request) bool {
				provider := strings.TrimSpace(route.Provider)
				if !modelbackend.BackendSupportsProvider(modelBackend, provider) {
					return false
				}
				if provider != modelbackend.BackendCopilotUser {
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
				DelegateTask: func(ctx context.Context, request modelgateway.TaskDelegationRequest) error {
					agent := strings.TrimSpace(request.Agent)
					if agent == "" {
						return fmt.Errorf("delegated agent is required")
					}
					if _, err := catalog.LoadAgentConfig(cfg.CatalogRoot, agent); err != nil {
						return err
					}
					_, err := sessionService.CreateDelegatedTaskSession(ctx, governance.DelegatedTaskSessionRequest{
						ParentSessionID:     request.ParentSessionID,
						ActorSubject:        request.ActorSubject,
						ParentAgent:         request.ParentAgent,
						Agent:               agent,
						Description:         request.Description,
						Prompt:              request.Prompt,
						ModelAlias:          request.ModelAlias,
						RequestedModelAlias: request.RequestedModelAlias,
						Provider:            request.Provider,
						ToolCallID:          request.ToolCallID,
						SourceSystem:        request.SourceSystem,
					})
					return err
				},
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
				gatewayConfig.FinishAutoSession = func(ctx context.Context, sessionID string, status string) error {
					return sessionStore.CompareAndSwapStatus(ctx, sessionID, "running", status)
				}
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
						AutoCreated:        true,
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
				logx.Infof("model compatibility gateway listening on %s", cfg.GatewayAddr)
				if err := gatewaySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logx.Fatalf("gateway error: %v", err)
				}
			}()
		}
	} else {
		logx.Infof("model compatibility gateway disabled: no runtime token configured")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if modelPricingStore != nil {
		governance.StartModelPricingRefresh(ctx, modelPricingStore, pricingFetcher, envDurationOrDefault("AI_ORCH_MODEL_PRICING_REFRESH_INTERVAL", 24*time.Hour), !pricingBootstrapped, logx.Infof)
	}

	go func() {
		logx.Infof("governance-shell listening on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logx.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	logx.Infof("shutting down gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logx.Fatalf("shutdown error: %v", err)
	}
	if gatewaySrv != nil {
		if err := gatewaySrv.Shutdown(shutdownCtx); err != nil {
			logx.Warnf("gateway shutdown error: %v", err)
		}
	}
	if modelPricingCloser != nil {
		if err := modelPricingCloser.Close(); err != nil {
			logx.Warnf("model pricing store close error: %v", err)
		}
	}
	if copilotStore != nil {
		if err := copilotStore.Close(); err != nil {
			logx.Warnf("copilot token store close error: %v", err)
		}
	}
	if killSwitchCloser != nil {
		if err := killSwitchCloser.Close(); err != nil {
			logx.Warnf("kill switch store close error: %v", err)
		}
	}
	if oauthTokenCloser != nil {
		if err := oauthTokenCloser.Close(); err != nil {
			logx.Warnf("oauth token store close error: %v", err)
		}
	}
	logx.Infof("shutdown complete")
}

type modelBackendHealth interface {
	Health(context.Context) error
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
