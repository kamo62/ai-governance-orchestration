package governance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
)

type MCPProxyRegistration struct {
	Endpoint          string
	AuthMode          string
	PlatformToken     string
	AllowedAgents     []string
	ToolAllow         []string
	ToolDeny          []string
	ClassificationMax string
}

type UserTokenStore interface {
	Token(ctx context.Context, userID string, serverID string) (string, bool)
}

type MCPProxyConfig struct {
	ServiceToken      string
	DevToken          string
	Authorizer        RequestAuthorizer
	Audit             audit.Store
	Sessions          SessionStore
	Registrations     map[string]MCPProxyRegistration
	UserTokens        UserTokenStore
	PolicyEngine      policyengine.Engine
	ClassificationMax string
	HTTPClient        *http.Client
	NewID             func(prefix string) string
}

type MCPProxyHandler struct {
	serviceToken      string
	devToken          string
	authorizer        RequestAuthorizer
	audit             audit.Store
	sessions          SessionStore
	registrations     map[string]MCPProxyRegistration
	userTokens        UserTokenStore
	policyEngine      policyengine.Engine
	classificationMax string
	httpClient        *http.Client
	newID             func(prefix string) string
}

func NewMCPProxyHandler(cfg MCPProxyConfig) http.Handler {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	engine := cfg.PolicyEngine
	if engine == nil {
		engine, _ = policyengine.New("native")
	}
	return &MCPProxyHandler{
		serviceToken:      cfg.ServiceToken,
		devToken:          cfg.DevToken,
		authorizer:        cfg.Authorizer,
		audit:             cfg.Audit,
		sessions:          cfg.Sessions,
		registrations:     cfg.Registrations,
		userTokens:        cfg.UserTokens,
		policyEngine:      engine,
		classificationMax: defaultString(cfg.ClassificationMax, "internal"),
		httpClient:        httpClient,
		newID:             newID,
	}
}

func (h *MCPProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authReq, auth, ok := h.requireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	// Catalog endpoint: list registered MCP servers and their tools.
	if r.Method == http.MethodGet && isMCPCatalogPath(r.URL.Path) {
		record, ok := h.requireSessionRecord(w, r, auth)
		if !ok {
			return
		}
		h.handleCatalog(w, r, record)
		return
	}

	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	serverID, toolName := parseMCPProxyPath(r.URL.Path)
	if serverID == "" || toolName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "path must be /internal/v1/mcp/{server_id}/tools/{tool_name}"})
		return
	}
	reg, ok := h.registrations[serverID]
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "unknown mcp server"})
		return
	}

	record, ok := h.requireSessionRecord(w, r, auth)
	if !ok {
		return
	}
	decision, err := h.authorizeTool(r.Context(), record, serverID, toolName, reg)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "policy evaluation failed"})
		return
	}
	if !decision.Allowed {
		if err := h.auditMCP(r.Context(), record, serverID, toolName, reg.AuthMode, "tool_call_denied", decision.DecisionID); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "tool_call_denied", "reason": decision.Reason, "decision_id": decision.DecisionID})
		return
	}

	authHeader, ok := h.authHeaderFor(r.Context(), reg, record.ActorSubject, serverID)
	if !ok {
		if err := h.auditMCP(r.Context(), record, serverID, toolName, reg.AuthMode, "oauth_user_token_missing", decision.DecisionID); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "oauth_user_token_missing"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "read request body: " + err.Error()})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(reg.Endpoint, "/")+"/"+toolName, bytes.NewReader(body))
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "create backend request failed"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	if err := h.auditMCP(r.Context(), record, serverID, toolName, reg.AuthMode, "forwarded", decision.DecisionID); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("mcp backend failed: %v", err)})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxRequestBodyBytes))

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

func (h *MCPProxyHandler) handleCatalog(w http.ResponseWriter, r *http.Request, record SessionRecord) {
	servers := make(map[string]map[string]any, len(h.registrations))
	for id, reg := range h.registrations {
		var allowedTools []string
		for _, toolName := range reg.ToolAllow {
			decision, err := h.authorizeTool(r.Context(), record, id, toolName, reg)
			if err != nil || !decision.Allowed {
				continue
			}
			allowedTools = append(allowedTools, toolName)
		}
		if len(allowedTools) == 0 {
			continue
		}
		servers[id] = map[string]any{
			"auth_mode": reg.AuthMode,
			"tools":     allowedTools,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"servers": servers})
}

func (h *MCPProxyHandler) requireSessionRecord(w http.ResponseWriter, r *http.Request, auth mcpProxyAuth) (SessionRecord, bool) {
	sessionID := r.Header.Get("X-AI-Orch-Session-ID")
	if sessionID == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id is required"})
		return SessionRecord{}, false
	}
	if h.sessions == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "durable session store is required for mcp proxy"})
		return SessionRecord{}, false
	}
	record, err := h.sessions.Get(r.Context(), sessionID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
		return SessionRecord{}, false
	}
	if !auth.Service && record.ActorSubject != auth.Subject {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
		return SessionRecord{}, false
	}
	return record, true
}

func (h *MCPProxyHandler) authorizeTool(ctx context.Context, record SessionRecord, serverID string, toolName string, reg MCPProxyRegistration) (policyengine.Decision, error) {
	classificationMax := strings.TrimSpace(reg.ClassificationMax)
	if classificationMax == "" {
		classificationMax = h.classificationMax
	}
	return h.policyEngine.Evaluate(ctx, policyengine.Request{
		SessionID:         record.SessionID,
		UserID:            record.ActorSubject,
		AgentName:         record.Agent,
		ActionType:        "mcp.tool_call",
		Resource:          serverID,
		ToolName:          toolName,
		Classification:    record.Classification,
		ClassificationMax: classificationMax,
		Metadata: map[string]any{
			"allowed_agents": reg.AllowedAgents,
			"tool_allow":     reg.ToolAllow,
			"tool_deny":      reg.ToolDeny,
		},
	})
}

func (h *MCPProxyHandler) authHeaderFor(ctx context.Context, reg MCPProxyRegistration, userID string, serverID string) (string, bool) {
	switch reg.AuthMode {
	case "", "none":
		return "", true
	case "local-dev-token":
		if reg.PlatformToken == "" {
			return "", false
		}
		return "Bearer " + reg.PlatformToken, true
	case "platform":
		if reg.PlatformToken == "" {
			return "", false
		}
		return "Bearer " + reg.PlatformToken, true
	case "oauth-user":
		if h.userTokens == nil {
			return "", false
		}
		token, ok := h.userTokens.Token(ctx, userID, serverID)
		if !ok || token == "" {
			return "", false
		}
		return "Bearer " + token, true
	default:
		return "", false
	}
}

func (h *MCPProxyHandler) auditMCP(ctx context.Context, record SessionRecord, serverID string, toolName string, authMode string, reason string, policyDecisionID string) error {
	if h == nil || h.audit == nil {
		return nil
	}
	_, err := h.audit.Append(ctx, audit.Event{
		EventID:            h.newID("evt"),
		SessionID:          record.SessionID,
		EventType:          "mcp.proxy_call",
		Actor:              "runtime",
		Agent:              record.Agent,
		Classification:     record.Classification,
		MCPServerID:        serverID,
		MCPToolName:        toolName,
		AuthMode:           authMode,
		Reason:             reason,
		PolicyDecisionID:   policyDecisionID,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         "gateway_enforced",
		EnforcementMode:    "gateway",
	})
	return err
}

type mcpProxyAuth struct {
	Subject string
	Service bool
}

func (h *MCPProxyHandler) requireAuthorizedRequest(w http.ResponseWriter, r *http.Request) (*http.Request, mcpProxyAuth, bool) {
	auth := r.Header.Get("Authorization")
	if h.serviceToken != "" && authorizedBearer(auth, h.serviceToken) {
		return r, mcpProxyAuth{Service: true}, true
	}
	if h.authorizer != nil {
		subject, ok := h.authorizer.Validate(r.Context(), auth)
		if !ok {
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return r, mcpProxyAuth{}, false
		}
		r = r.WithContext(WithAuthInfo(r.Context(), AuthInfo{Subject: subject, Method: "oidc"}))
		return r, mcpProxyAuth{Subject: subject}, true
	}
	if h.devToken != "" && authorizedBearer(auth, h.devToken) {
		subject := "local-dev"
		if localIdentity := r.Header.Get("X-AI-Orch-Local-Identity"); localIdentity != "" {
			if !validActorLabel(localIdentity) {
				httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid local identity"})
				return r, mcpProxyAuth{}, false
			}
			subject = localIdentity
		}
		r = r.WithContext(WithAuthInfo(r.Context(), AuthInfo{Subject: subject, Method: "dev"}))
		return r, mcpProxyAuth{Subject: subject}, true
	}
	httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	return r, mcpProxyAuth{}, false
}

func isMCPCatalogPath(path string) bool {
	return path == "/internal/v1/mcp/catalog" || path == "/v1/mcp/catalog"
}

func parseMCPProxyPath(path string) (serverID string, toolName string) {
	var rest string
	switch {
	case strings.HasPrefix(path, "/internal/v1/mcp/"):
		rest = strings.TrimPrefix(path, "/internal/v1/mcp/")
	case strings.HasPrefix(path, "/v1/mcp/"):
		rest = strings.TrimPrefix(path, "/v1/mcp/")
	default:
		return "", ""
	}
	if rest == "catalog" {
		return "", ""
	}
	parts := strings.Split(rest, "/tools/")
	if len(parts) != 2 {
		return "", ""
	}
	if parts[0] == "" || parts[1] == "" || strings.Contains(parts[0], "/") || strings.Contains(parts[1], "/") {
		return "", ""
	}
	return parts[0], parts[1]
}
