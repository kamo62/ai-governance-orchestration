package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"ai-agent-orch/internal/appconfig"
	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/httpauth"
	"ai-agent-orch/internal/orchestrator"
	"ai-agent-orch/internal/server"
)

func main() {
	cfg, err := appconfig.Load(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	baseHandler := server.New("orchestrator", func() error {
		_, err := catalog.Validate(cfg.CatalogRoot)
		return err
	}, func() (server.CatalogSummary, error) {
		report, err := catalog.Validate(cfg.CatalogRoot)
		if err != nil {
			return server.CatalogSummary{}, err
		}
		return server.CatalogSummary{Agents: len(report.Agents), Models: len(report.ModelAliases)}, nil
	})

	auditStore, err := audit.NewStore(cfg.AuditPath)
	if err != nil {
		log.Fatal(err)
	}
	sessionIntake := orchestrator.NewSessionIntake(orchestrator.SessionIntakeConfig{
		Audit: auditStore,
	})
	router := orchestrator.NewRouter(orchestrator.RouterConfig{
		CatalogRoot: cfg.CatalogRoot,
		Audit:       auditStore,
	})
	dispatcher := orchestrator.NewDispatcher(cfg.CatalogRoot)

	handler := http.NewServeMux()
	handler.Handle("/", baseHandler)
	handler.Handle("/v1/orchestrator/sessions", httpauth.RequireBearerToken(cfg.ServiceToken, orchestrator.NewSessionIntakeHandler(sessionIntake)))
	handler.Handle("/v1/orchestrator/route", httpauth.RequireBearerToken(cfg.ServiceToken, orchestrator.NewRouterHandler(router)))
	handler.Handle("/v1/orchestrator/dispatch", httpauth.RequireBearerToken(cfg.ServiceToken, orchestrator.NewDispatchHandler(dispatcher, auditStore)))

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	log.Printf("orchestrator listening on %s", cfg.Addr)
	log.Fatal(srv.ListenAndServe())
}
