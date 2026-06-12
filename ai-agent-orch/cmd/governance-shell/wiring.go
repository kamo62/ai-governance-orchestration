package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/contextresolver"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/copilot"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/governance"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/logx"
)

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

// sessionSubrouter dispatches /v1/sessions/{id}/messages, /confirm, /patch-decision, /events.
type sessionSubrouter struct {
	sessionService *governance.SessionService
	orchClient     governance.OrchestratorClient
	events         *governance.EventStore
}

func defaultMCPRegistrations(catalogRoot string, classificationMax string) map[string]governance.MCPProxyRegistration {
	platformToken := os.Getenv("AI_ORCH_MCP_TOKEN")
	registrations, err := catalog.LoadMCPRegistrations(catalogRoot)
	if err != nil {
		logx.Fatalf("load mcp registrations: %v", err)
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
		logx.Infof("using sqlite audit store: %s", auditPath)
	}
	return store, nil
}

func hasSQLiteExt(path string) bool {
	return audit.IsSQLitePath(path)
}
