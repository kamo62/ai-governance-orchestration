package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"ai-agent-orch/internal/appconfig"
	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/governance"
	"ai-agent-orch/internal/httpauth"
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
	killSwitchStore := governance.NewMemoryKillSwitch()
	metricsHandler := governance.NewMetricsHandler()

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
		Metrics:           metricsHandler,
	})
	eventStore := governance.NewEventStore()

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
	handler.Handle("/metrics", metricsHandler)

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Printf("governance-shell listening on %s", cfg.Addr)
	log.Fatal(srv.ListenAndServe())
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
	case containsSuffix(path, "/events"):
		governance.NewEventsHandler(sr.events).ServeHTTP(w, r)
	case containsSuffix(path, "/abort"):
		governance.NewAbortHandler(sr.sessionService, sr.events).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func containsSuffix(path, suffix string) bool {
	return len(path) > len(suffix) && path[len(path)-len(suffix):] == suffix
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
