// Package modelgateway implements the OpenAI-compatible Model Compatibility Gateway.
// It exposes governed model aliases to runtimes without handing them provider keys.
package modelgateway

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/openrouter"
	"ai-agent-orch/internal/router"
)

// GatewayConfig holds the configuration for the model compatibility gateway.
type GatewayConfig struct {
	RuntimeToken    string
	Router          *router.Router
	OpenRouter      openrouter.ChatClient
	Audit           audit.Store
	NewID           func(prefix string) string
	ValidateSession func(context.Context, string) error
}

// Gateway is an OpenAI-compatible model endpoint owned by the Governance Shell.
type Gateway struct {
	runtimeToken    string
	router          *router.Router
	openRouter      openrouter.ChatClient
	audit           audit.Store
	newID           func(prefix string) string
	validateSession func(context.Context, string) error
}

// NewGateway creates a new model compatibility gateway.
func NewGateway(cfg GatewayConfig) *Gateway {
	newID := cfg.NewID
	if newID == nil {
		newID = func(prefix string) string {
			// In production this should use crypto/rand.
			// Using timestamp-based fallback for deterministic tests.
			return prefix + "_" + fmt.Sprintf("%x", time.Now().UnixNano())[:16]
		}
	}
	return &Gateway{
		runtimeToken:    cfg.RuntimeToken,
		router:          cfg.Router,
		openRouter:      cfg.OpenRouter,
		audit:           cfg.Audit,
		newID:           newID,
		validateSession: cfg.ValidateSession,
	}
}

// Handler returns an http.Handler for the gateway endpoints.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", g.handleModels)
	mux.HandleFunc("/v1/chat/completions", g.handleChatCompletions)
	mux.HandleFunc("/v1/responses", g.handleResponses)
	return mux
}

func (g *Gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !g.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if g.router == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "router unavailable"})
		return
	}

	classification := r.URL.Query().Get("classification")
	if classification == "" {
		classification = "internal" // default for runtime listings
	}

	models := g.router.Aliases(classification)
	data := make([]modelListEntry, 0, len(models))
	for _, m := range models {
		data = append(data, modelListEntry{
			ID:      m.Alias,
			Object:  "model",
			OwnedBy: "ai-orch",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

type modelListEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

func (g *Gateway) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !g.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if g.router == nil || g.openRouter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "gateway unavailable"})
		return
	}
	sessionID, ok := requiredSessionID(w, r)
	if !ok {
		return
	}
	if !g.sessionValid(w, r, sessionID) {
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // 1 MiB max
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "read body: " + err.Error()})
		return
	}

	var req openAIChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "model is required"})
		return
	}
	if len(req.Messages) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "messages are required"})
		return
	}

	// Route alias to concrete model.
	classification := r.Header.Get("X-AI-Orch-Classification")
	if classification == "" {
		classification = "internal"
	}
	decision, err := g.router.Route(r.Context(), router.Request{
		TaskType:       inferTaskType(req.Messages),
		Classification: classification,
		PreferredAlias: req.Model,
	})
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}

	// Build upstream request.
	upstream := openrouter.ChatCompletionRequest{
		ModelAlias:  decision.SelectedAlias,
		Model:       decision.SelectedModelID,
		Messages:    convertMessages(req.Messages),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	if req.Stream {
		g.handleStream(w, r, upstream, decision, sessionID, body)
		return
	}

	resp, err := g.openRouter.ChatCompletion(r.Context(), upstream)
	if err != nil {
		g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_call", body, nil, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}

	respBody, _ := json.Marshal(resp)
	g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_call", body, respBody, "")

	openAIResp := openAIChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Model:   decision.SelectedAlias, // return alias, not provider model
		Choices: convertChoices(resp.Choices),
		Usage: openAIUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	writeJSON(w, http.StatusOK, openAIResp)
}

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request, upstream openrouter.ChatCompletionRequest, decision router.Decision, sessionID string, reqBody []byte) {
	streamReader, err := g.openRouter.ChatCompletionStream(r.Context(), upstream)
	if err != nil {
		g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_stream", reqBody, nil, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("stream start failed: %v", err)})
		return
	}
	defer streamReader.Close()

	g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_stream", reqBody, nil, "")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	scanner := bufio.NewScanner(streamReader)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		chunk, err := openrouter.DecodeStreamChunk(line)
		if err != nil {
			if errors.Is(err, io.EOF) || line == "data: [DONE]" {
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				break
			}
			continue // skip malformed lines
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		// Translate provider chunk to OpenAI-compatible chunk with alias.
		openAIChunk := openAIStreamChunk{
			ID:     chunk.ID,
			Object: "chat.completion.chunk",
			Model:  decision.SelectedAlias,
			Choices: []openAIStreamChoice{
				{
					Index: chunk.Choices[0].Index,
					Delta: openAIMessageDelta{
						Role:    chunk.Choices[0].Delta.Role,
						Content: chunk.Choices[0].Delta.Content,
					},
					FinishReason: chunk.Choices[0].FinishReason,
				},
			},
		}

		data, _ := json.Marshal(openAIChunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
}

func (g *Gateway) handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !g.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if g.router == nil || g.openRouter == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "gateway unavailable"})
		return
	}
	sessionID, ok := requiredSessionID(w, r)
	if !ok {
		return
	}
	if !g.sessionValid(w, r, sessionID) {
		return
	}

	// Phase 1G.5: Responses API MVP.
	// For now, map responses to chat completions internally.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "read body: " + err.Error()})
		return
	}

	var req openAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "model is required"})
		return
	}
	if len(req.Input) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "input is required"})
		return
	}

	classification := r.Header.Get("X-AI-Orch-Classification")
	if classification == "" {
		classification = "internal"
	}
	decision, err := g.router.Route(r.Context(), router.Request{
		TaskType:       inferTaskTypeFromInput(req.Input),
		Classification: classification,
		PreferredAlias: req.Model,
	})
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}

	upstream := openrouter.ChatCompletionRequest{
		ModelAlias: decision.SelectedAlias,
		Model:      decision.SelectedModelID,
		Messages:   convertResponsesInput(req.Input),
	}

	resp, err := g.openRouter.ChatCompletion(r.Context(), upstream)
	if err != nil {
		g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_responses", body, nil, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}

	respBody, _ := json.Marshal(resp)
	g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_responses", body, respBody, "")

	openAIResp := openAIResponsesResponse{
		ID:     g.newID("resp"),
		Object: "response",
		Model:  decision.SelectedAlias,
		Output: []openAIResponseOutput{
			{
				Type: "message",
				Content: []openAIResponseContent{
					{Type: "output_text", Text: resp.FirstContent()},
				},
			},
		},
		Usage: openAIUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	writeJSON(w, http.StatusOK, openAIResp)
}

func requiredSessionID(w http.ResponseWriter, r *http.Request) (string, bool) {
	sessionID := strings.TrimSpace(r.Header.Get("X-AI-Orch-Session-ID"))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "X-AI-Orch-Session-ID header is required"})
		return "", false
	}
	return sessionID, true
}

func (g *Gateway) sessionValid(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if g.validateSession == nil {
		return true
	}
	if err := g.validateSession(r.Context(), sessionID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
		return false
	}
	return true
}

func (g *Gateway) authorized(r *http.Request) bool {
	if g.runtimeToken == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return subtleStrEq(strings.TrimPrefix(auth, prefix), g.runtimeToken)
}

func subtleStrEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func (g *Gateway) auditModelCall(ctx context.Context, sessionID string, decision router.Decision, eventType string, reqBody, respBody []byte, errMsg string) {
	if g.audit == nil {
		return
	}
	var reqHash, respHash string
	if len(reqBody) > 0 {
		reqHash = sha256Hex(reqBody)
	}
	if len(respBody) > 0 {
		respHash = sha256Hex(respBody)
	}
	reason := ""
	if errMsg != "" {
		reason = errMsg
	}
	_, _ = g.audit.Append(ctx, audit.Event{
		EventID:            g.newID("evt"),
		SessionID:          sessionID,
		EventType:          eventType,
		Actor:              "runtime",
		Provider:           decision.Provider,
		ModelAlias:         decision.SelectedAlias,
		ModelResolved:      decision.SelectedModelID,
		RequestSHA256:      reqHash,
		ResponseSHA256:     respHash,
		Reason:             reason,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "model-gateway",
	})
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func inferTaskType(messages []openAIMessage) string {
	if len(messages) == 0 {
		return "general"
	}
	content := strings.ToLower(messages[len(messages)-1].Content)
	switch {
	case strings.Contains(content, "test") || strings.Contains(content, "spec"):
		return "test"
	case strings.Contains(content, "review") || strings.Contains(content, "audit"):
		return "review"
	case strings.Contains(content, "refactor") || strings.Contains(content, "architecture"):
		return "architecture"
	case strings.Contains(content, "implement") || strings.Contains(content, "code"):
		return "coding"
	default:
		return "general"
	}
}

func inferTaskTypeFromInput(input []openAIResponsesInput) string {
	if len(input) == 0 {
		return "general"
	}
	content := strings.ToLower(input[len(input)-1].Content)
	switch {
	case strings.Contains(content, "test") || strings.Contains(content, "spec"):
		return "test"
	case strings.Contains(content, "review") || strings.Contains(content, "audit"):
		return "review"
	case strings.Contains(content, "refactor") || strings.Contains(content, "architecture"):
		return "architecture"
	case strings.Contains(content, "implement") || strings.Contains(content, "code"):
		return "coding"
	default:
		return "general"
	}
}

func convertMessages(msgs []openAIMessage) []openrouter.Message {
	out := make([]openrouter.Message, len(msgs))
	for i, m := range msgs {
		out[i] = openrouter.Message{Role: m.Role, Content: m.Content}
	}
	return out
}

func convertChoices(choices []struct {
	Message openrouter.Message `json:"message"`
}) []openAIChoice {
	out := make([]openAIChoice, len(choices))
	for i, c := range choices {
		out[i] = openAIChoice{
			Index:   i,
			Message: openAIMessage{Role: c.Message.Role, Content: c.Message.Content},
		}
	}
	return out
}

func convertResponsesInput(input []openAIResponsesInput) []openrouter.Message {
	out := make([]openrouter.Message, 0, len(input))
	for _, item := range input {
		role := item.Role
		if role == "" {
			role = "user"
		}
		out = append(out, openrouter.Message{Role: role, Content: item.Content})
	}
	return out
}

// OpenAI-compatible request/response types.

type openAIChatCompletionRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatCompletionResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

type openAIChoice struct {
	Index   int           `json:"index"`
	Message openAIMessage `json:"message"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Model   string               `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
}

type openAIStreamChoice struct {
	Index        int                `json:"index"`
	Delta        openAIMessageDelta `json:"delta"`
	FinishReason *string            `json:"finish_reason,omitempty"`
}

type openAIMessageDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// OpenAI Responses API types.

type openAIResponsesRequest struct {
	Model string                 `json:"model"`
	Input []openAIResponsesInput `json:"input"`
}

type openAIResponsesInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponsesResponse struct {
	ID     string                 `json:"id"`
	Object string                 `json:"object"`
	Model  string                 `json:"model"`
	Output []openAIResponseOutput `json:"output"`
	Usage  openAIUsage            `json:"usage"`
}

type openAIResponseOutput struct {
	Type    string                  `json:"type"`
	Content []openAIResponseContent `json:"content"`
}

type openAIResponseContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
