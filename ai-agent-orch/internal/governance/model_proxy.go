package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/openrouter"
)

type ModelProxyConfig struct {
	ServiceToken string
	OpenRouter   openrouter.ChatClient
	Audit        audit.Store
	NewID        func(prefix string) string
}

type ModelProxyHandler struct {
	serviceToken string
	openRouter   openrouter.ChatClient
	audit        audit.Store
	newID        func(prefix string) string
}

func NewModelProxyHandler(cfg ModelProxyConfig) http.Handler {
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	return &ModelProxyHandler{
		serviceToken: cfg.ServiceToken,
		openRouter:   cfg.OpenRouter,
		audit:        cfg.Audit,
		newID:        newID,
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
	if h.openRouter == nil {
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

	resp, err := h.openRouter.ChatCompletion(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}
	respBody, err := json.Marshal(resp)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "encode model response failed"})
		return
	}

	if h.audit != nil {
		if _, err := h.audit.Append(r.Context(), audit.Event{
			EventID:            h.newID("evt"),
			SessionID:          sessionID,
			EventType:          "model.proxy_call",
			Actor:              "runtime",
			Provider:           "openrouter",
			ModelAlias:         r.Header.Get("X-AI-Orch-Model-Alias"),
			ModelResolved:      req.Model,
			ProxyCallID:        h.newID("proxy"),
			RequestSHA256:      sha256Hex(body),
			ResponseSHA256:     sha256Hex(respBody),
			TokenUsage:         tokenUsage(resp.Usage),
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

var _ http.Handler = (*ModelProxyHandler)(nil)
