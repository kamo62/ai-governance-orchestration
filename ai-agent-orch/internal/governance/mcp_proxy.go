package governance

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"ai-agent-orch/internal/audit"
)

type MCPProxyRegistration struct {
	Endpoint      string
	AuthMode      string
	PlatformToken string
}

type UserTokenStore interface {
	Token(ctx context.Context, userID string, serverID string) (string, bool)
}

type StaticUserTokenStore map[string]string

func (s StaticUserTokenStore) Token(_ context.Context, userID string, serverID string) (string, bool) {
	token, ok := s[userID+"|"+serverID]
	return token, ok
}

type MCPProxyConfig struct {
	ServiceToken  string
	Audit         audit.Store
	Registrations map[string]MCPProxyRegistration
	UserTokens    UserTokenStore
	HTTPClient    *http.Client
	NewID         func(prefix string) string
}

type MCPProxyHandler struct {
	serviceToken  string
	audit         audit.Store
	registrations map[string]MCPProxyRegistration
	userTokens    UserTokenStore
	httpClient    *http.Client
	newID         func(prefix string) string
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
	return &MCPProxyHandler{
		serviceToken:  cfg.ServiceToken,
		audit:         cfg.Audit,
		registrations: cfg.Registrations,
		userTokens:    cfg.UserTokens,
		httpClient:    httpClient,
		newID:         newID,
	}
}

func (h *MCPProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.serviceToken == "" || !authorizedBearer(r.Header.Get("Authorization"), h.serviceToken) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}

	serverID, toolName := parseMCPProxyPath(r.URL.Path)
	if serverID == "" || toolName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path must be /internal/v1/mcp/{server_id}/tools/{tool_name}"})
		return
	}
	reg, ok := h.registrations[serverID]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown mcp server"})
		return
	}

	sessionID := r.Header.Get("X-AI-Orch-Session-ID")
	userID := r.Header.Get("X-AI-Orch-User-ID")
	authHeader, ok := h.authHeaderFor(r.Context(), reg, userID, serverID)
	if !ok {
		h.auditMCP(r.Context(), sessionID, serverID, toolName, reg.AuthMode, "oauth_user_token_missing")
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "oauth_user_token_missing"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "read request body: " + err.Error()})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(reg.Endpoint, "/")+"/tools/"+toolName, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "create backend request failed"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("mcp backend failed: %v", err)})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxRequestBodyBytes))

	h.auditMCP(r.Context(), sessionID, serverID, toolName, reg.AuthMode, "forwarded")
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

func (h *MCPProxyHandler) authHeaderFor(ctx context.Context, reg MCPProxyRegistration, userID string, serverID string) (string, bool) {
	switch reg.AuthMode {
	case "", "none", "local-dev-token":
		return "", true
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

func (h *MCPProxyHandler) auditMCP(ctx context.Context, sessionID string, serverID string, toolName string, authMode string, reason string) {
	if h == nil || h.audit == nil {
		return
	}
	_, _ = h.audit.Append(ctx, audit.Event{
		EventID:            h.newID("evt"),
		SessionID:          sessionID,
		EventType:          "mcp.proxy_call",
		Actor:              "runtime",
		MCPServerID:        serverID,
		MCPToolName:        toolName,
		AuthMode:           authMode,
		Reason:             reason,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
	})
}

func parseMCPProxyPath(path string) (serverID string, toolName string) {
	const prefix = "/internal/v1/mcp/"
	if !strings.HasPrefix(path, prefix) {
		return "", ""
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(rest, "/tools/")
	if len(parts) != 2 {
		return "", ""
	}
	if parts[0] == "" || parts[1] == "" || strings.Contains(parts[0], "/") || strings.Contains(parts[1], "/") {
		return "", ""
	}
	return parts[0], parts[1]
}
