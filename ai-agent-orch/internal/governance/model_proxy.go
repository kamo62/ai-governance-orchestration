package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/modelbackend"
	"ai-agent-orch/internal/openrouter"
)

type ModelProxyConfig struct {
	ServiceToken  string
	Backend       modelbackend.Backend
	Audit         audit.Store
	LookupSession func(context.Context, string) (SessionRecord, error)
	NewID         func(prefix string) string
}

type ModelProxyHandler struct {
	serviceToken  string
	backend       modelbackend.Backend
	audit         audit.Store
	lookupSession func(context.Context, string) (SessionRecord, error)
	newID         func(prefix string) string
}

func NewModelProxyHandler(cfg ModelProxyConfig) http.Handler {
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	return &ModelProxyHandler{
		serviceToken:  cfg.ServiceToken,
		backend:       cfg.Backend,
		audit:         cfg.Audit,
		lookupSession: cfg.LookupSession,
		newID:         newID,
	}
}

func (h *ModelProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if r.URL.Path != "/internal/v1/model/chat" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if h.serviceToken == "" || !authorizedBearer(r.Header.Get("Authorization"), h.serviceToken) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if h.backend == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "model proxy unavailable"})
		return
	}
	sessionID := r.Header.Get("X-AI-Orch-Session-ID")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "X-AI-Orch-Session-ID header is required"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "read request body: " + err.Error()})
		return
	}
	var req openrouter.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if provider := r.Header.Get("X-AI-Orch-Provider"); provider != "" {
		req.Provider = provider
	}
	var record SessionRecord
	if h.lookupSession != nil {
		var err error
		record, err = h.lookupSession(r.Context(), sessionID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return
		}
	}

	respBody, resolvedModel, usage, err := h.callBackend(r.Context(), req, body, record)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}

	if h.audit != nil {
		if _, err := h.audit.Append(r.Context(), audit.Event{
			EventID:   h.newID("evt"),
			SessionID: sessionID,
			EventType: "model.proxy_call",
			Actor:     "runtime",
			Provider:  req.Provider,
			// ModelAlias is passed through from the orchestrator for audit correlation.
			// Alias-to-model enforcement is currently delegated to the orchestrator;
			// the proxy trusts req.Model as the resolved value.
			ModelAlias:         r.Header.Get("X-AI-Orch-Model-Alias"),
			ModelResolved:      resolvedModel,
			ProxyCallID:        h.newID("proxy"),
			RequestSHA256:      sha256Hex(body),
			ResponseSHA256:     sha256Hex(respBody),
			TokenUsage:         usage,
			GatewayBackend:     h.backend.Name(),
			TrustLevel:         "gateway_enforced",
			EnforcementMode:    "gateway",
			RawPromptStored:    false,
			RawResponseStored:  false,
			CorrelationSubject: "governance-shell",
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(respBody)
}

func (h *ModelProxyHandler) callBackend(ctx context.Context, req openrouter.ChatCompletionRequest, body []byte, record SessionRecord) ([]byte, string, map[string]any, error) {
	if rawBackend, ok := h.backend.(modelbackend.RawChatBackend); ok {
		respBody, err := rawBackend.ChatCompletionRaw(ctx, modelbackend.RawRequest{
			Provider:     req.Provider,
			ModelAlias:   req.ModelAlias,
			Model:        req.Model,
			Body:         body,
			ActorSubject: record.ActorSubject,
		})
		if err != nil {
			return nil, "", nil, err
		}
		return respBody, h.backend.ResolvedModel(req.Provider, req.Model), usageFromRawProxyResponse(respBody), nil
	}
	resp, err := h.backend.ChatCompletion(ctx, req)
	if err != nil {
		return nil, "", nil, err
	}
	respBody, err := json.Marshal(resp)
	if err != nil {
		return nil, "", nil, err
	}
	return respBody, h.backend.ResolvedModel(req.Provider, req.Model), tokenUsage(resp.Usage), nil
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func tokenUsage(usage openrouter.Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
		"cost_usd":          usage.Cost,
		"reasoning_tokens":  usage.CompletionTokensDetails.ReasoningTokens,
	}
}

func usageFromRawProxyResponse(body []byte) map[string]any {
	var payload struct {
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Usage == nil {
		return map[string]any{}
	}
	return payload.Usage
}

var _ http.Handler = (*ModelProxyHandler)(nil)
