package main

import (
	"log"
	"net/http"
	"os"

	"ai-agent-orch/internal/appconfig"
	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/catalog"
	"ai-agent-orch/internal/governance"
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

	auditStore := audit.NewFileStore(cfg.AuditPath)
	sessionService := governance.NewSessionService(governance.SessionConfig{
		DevToken:          cfg.DevToken,
		Audit:             auditStore,
		ClassificationMax: cfg.ClassificationMax,
		KillSwitch:        cfg.KillSwitch,
		CostCapEnabled:    cfg.CostCapEnabled,
		SessionCostCapUSD: cfg.SessionCostCapUSD,
	})
	handler := http.NewServeMux()
	handler.Handle("/", baseHandler)
	handler.Handle("/v1/sessions", governance.NewSessionHandler(sessionService))
	handler.Handle("/v1/audit/sessions/", governance.NewAuditLookupHandler(governance.AuditLookupConfig{
		DevToken: cfg.DevToken,
		Audit:    auditStore,
	}))

	log.Printf("governance-shell listening on %s", cfg.Addr)
	log.Fatal(http.ListenAndServe(cfg.Addr, handler))
}
