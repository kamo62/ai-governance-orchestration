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

type authMode string

const (
	authRequired     authMode = "required"
	authAdmin        authMode = "admin"
	authAdminOnWrite authMode = "admin_on_write"
	authPublic       authMode = "public"
	authSelf         authMode = "self"
)

type authRouter struct {
	mux   *http.ServeMux
	modes map[string]authMode
}

func newAuthRouter() *authRouter {
	return &authRouter{mux: http.NewServeMux(), modes: map[string]authMode{}}
}

func (r *authRouter) Handle(mode authMode, pattern string, handler http.Handler) {
	r.mux.Handle(pattern, handler)
	r.modes[pattern] = mode
}

func (r *authRouter) Handler(service *governance.SessionService) http.Handler {
	return authModeMiddleware(service, r.mux, r.modes)
}

// authModeMiddleware authorizes each matched route from its registration mode.
// A route registered directly on the mux has no mode and is denied by default.
func authModeMiddleware(service *governance.SessionService, mux *http.ServeMux, modes map[string]authMode) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		next, pattern := mux.Handler(req)
		mode, ok := modes[pattern]
		if !ok {
			http.Error(w, "route auth mode required", http.StatusUnauthorized)
			return
		}

		switch mode {
		case authPublic, authSelf:
			next.ServeHTTP(w, req)
			return
		case authAdmin:
			if service.RequireAdminRequest(w, req) {
				next.ServeHTTP(w, req)
			}
			return
		case authRequired, authAdminOnWrite:
			if mode == authAdminOnWrite && req.Method != http.MethodGet {
				if service.RequireAdminRequest(w, req) {
					next.ServeHTTP(w, req)
				}
				return
			}
			if subject, ok := service.AdminBearerSubject(req.Header.Get("Authorization")); ok {
				next.ServeHTTP(w, req.WithContext(governance.WithAuthInfo(req.Context(), governance.AuthInfo{Subject: subject, Method: "admin"})))
				return
			}
			authReq, ok := service.RequireAuthorizedRequest(w, req)
			if ok {
				next.ServeHTTP(w, authReq)
			}
			return
		default:
			http.Error(w, "route auth mode required", http.StatusUnauthorized)
		}
	})
}

func registerRegistryHandlers(mux *authRouter, registryHandler http.Handler) {
	mux.Handle(authRequired, "/v1/use-cases", registryHandler)
	mux.Handle(authRequired, "/v1/use-cases/", registryHandler)
	mux.Handle(authRequired, "/v1/workflows", registryHandler)
	mux.Handle(authRequired, "/v1/context-manifests", registryHandler)
	mux.Handle(authRequired, "/v1/context-manifests/", registryHandler)
	mux.Handle(authRequired, "/v1/reporting/maturity-governance", registryHandler)
	mux.Handle(authRequired, "/v1/cache-outcomes", registryHandler)
	mux.Handle(authRequired, "/v1/evidence", registryHandler)
}

func registerAdminRegistryHandlers(mux *authRouter, adminRegistryHandler http.Handler) {
	mux.Handle(authAdmin, "/v1/admin/evidence", adminRegistryHandler)
	mux.Handle(authAdmin, "/v1/admin/evidence/", adminRegistryHandler)
	mux.Handle(authAdmin, "/v1/admin/cache-outcomes", adminRegistryHandler)
	mux.Handle(authAdmin, "/v1/admin/reporting/maturity-governance", adminRegistryHandler)
}

// registerInsightHandlers wires the read-only governance insight projection
// (actor-scoped and admin) alongside the existing reporting routes, plus the
// explicit admin-triggered maturity snapshot that materializes the same
// window into maturity_exports.
func registerInsightHandlers(mux *authRouter, insightHandler, adminInsightHandler, maturityExportRunHandler http.Handler) {
	mux.Handle(authRequired, "/v1/reporting/governance-insights", insightHandler)
	mux.Handle(authAdmin, "/v1/admin/reporting/governance-insights", adminInsightHandler)
	mux.Handle(authAdmin, "/v1/admin/reporting/maturity-export/run", maturityExportRunHandler)
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

func managedClientReceiptStore(store governance.PolicyDecisionStore) governance.ManagedClientReceiptStore {
	receipts, _ := store.(governance.ManagedClientReceiptStore)
	return receipts
}
