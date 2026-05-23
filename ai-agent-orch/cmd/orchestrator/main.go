package main

import (
	"log"
	"net/http"
	"os"

	"ai-agent-orch/internal/appconfig"
	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/catalog"
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

	auditStore := audit.NewFileStore(cfg.AuditPath)
	sessionIntake := orchestrator.NewSessionIntake(orchestrator.SessionIntakeConfig{
		Audit: auditStore,
	})
	router := orchestrator.NewRouter(orchestrator.RouterConfig{
		CatalogRoot: cfg.CatalogRoot,
		Audit:       auditStore,
	})
	handler := http.NewServeMux()
	handler.Handle("/", baseHandler)
	handler.Handle("/v1/orchestrator/sessions", orchestrator.NewSessionIntakeHandler(sessionIntake))
	handler.Handle("/v1/orchestrator/route", orchestrator.NewRouterHandler(router))

	log.Printf("orchestrator listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, handler))
}
