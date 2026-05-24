package main

import (
	"log"
	"net/http"
	"os"
	"strings"

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

	sessionService := governance.NewSessionService(governance.SessionConfig{
		DevToken:          cfg.DevToken,
		Authorizer:        requestAuthorizer,
		Audit:             auditStore,
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

	log.Printf("governance-shell listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, handler))
}

// sessionSubrouter dispatches /v1/sessions/{id}/messages, /confirm, /patch-decision, /events.
type sessionSubrouter struct {
	sessionService *governance.SessionService
	orchClient     governance.OrchestratorClient
	events         *governance.EventStore
}

func (sr *sessionSubrouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !sr.sessionService.RequireAuthorizedRequest(w, r) {
		return
	}

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
	default:
		http.NotFound(w, r)
	}
}

func containsSuffix(path, suffix string) bool {
	return len(path) > len(suffix) && path[len(path)-len(suffix):] == suffix
}

func newAuditStore(auditPath string) (audit.Store, error) {
	// If the path ends with .db or .sqlite, use SQLite.
	// Otherwise default to the append-only JSONL file store.
	if hasSQLiteExt(auditPath) {
		store, err := audit.NewSQLiteStore(auditPath)
		if err != nil {
			return nil, err
		}
		log.Printf("using sqlite audit store: %s", auditPath)
		return store, nil
	}
	return audit.NewFileStore(auditPath), nil
}

func hasSQLiteExt(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".sqlite3")
}
