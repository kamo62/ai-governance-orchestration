package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
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
	chainAudit := audit.NewChainAppender(auditStore)
	sessionIntake := orchestrator.NewSessionIntake(orchestrator.SessionIntakeConfig{
		Audit: chainAudit,
	})
	router := orchestrator.NewRouter(orchestrator.RouterConfig{
		CatalogRoot: cfg.CatalogRoot,
		Audit:       chainAudit,
	})
	dispatcher := orchestrator.NewDispatcher(cfg.CatalogRoot)

	handler := http.NewServeMux()
	handler.Handle("/", baseHandler)
	handler.Handle("/v1/orchestrator/sessions", httpauth.RequireBearerToken(cfg.ServiceToken, orchestrator.NewSessionIntakeHandler(sessionIntake)))
	handler.Handle("/v1/orchestrator/route", httpauth.RequireBearerToken(cfg.ServiceToken, orchestrator.NewRouterHandler(router)))
	handler.Handle("/v1/orchestrator/dispatch", httpauth.RequireBearerToken(cfg.ServiceToken, orchestrator.NewDispatchHandler(dispatcher, chainAudit)))

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
	log.Printf("orchestrator listening on %s", cfg.Addr)
	log.Fatal(srv.ListenAndServe())
}

// logRequestLatency wraps an http.Handler, tags requests with an ID, and logs
// requests that exceed a threshold. The governance shell forwards its own
// X-Request-ID so cross-service calls share one correlation ID.
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
