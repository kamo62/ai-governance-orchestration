// Package modelgateway implements the OpenAI-compatible Model Compatibility Gateway.
// It exposes governed model aliases to runtimes without handing them provider keys.
package modelgateway

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/httpauth"
	"ai-agent-orch/internal/modelbackend"
	"ai-agent-orch/internal/openrouter"
	"ai-agent-orch/internal/router"
)

// GatewayConfig holds the configuration for the model compatibility gateway.
type GatewayConfig struct {
	RuntimeToken    string
	Router          *router.Router
	Backend         modelbackend.Backend
	OpenRouter      openrouter.ChatClient
	Audit           audit.Store
	NewID           func(prefix string) string
	ValidateSession func(context.Context, string) error
	LookupSession   func(context.Context, string) (SessionInfo, error)
}

// SessionInfo is server-side session context used by runtime model routing.
type SessionInfo struct {
	Classification string
}

// Gateway is an OpenAI-compatible model endpoint owned by the Governance Shell.
type Gateway struct {
	runtimeToken    string
	router          *router.Router
	backend         modelbackend.Backend
	audit           audit.Store
	newID           func(prefix string) string
	validateSession func(context.Context, string) error
	lookupSession   func(context.Context, string) (SessionInfo, error)
}

// NewGateway creates a new model compatibility gateway.
func NewGateway(cfg GatewayConfig) *Gateway {
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	backend := cfg.Backend
	if backend == nil && cfg.OpenRouter != nil {
		backend = modelbackend.NewOpenRouterBackend(cfg.OpenRouter)
	}
	return &Gateway{
		runtimeToken:    cfg.RuntimeToken,
		router:          cfg.Router,
		backend:         backend,
		audit:           cfg.Audit,
		newID:           newID,
		validateSession: cfg.ValidateSession,
		lookupSession:   cfg.LookupSession,
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
	if g.router == nil || g.backend == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "gateway unavailable"})
		return
	}
	sessionID, ok := requiredSessionID(w, r)
	if !ok {
		return
	}
	session, ok := g.sessionInfo(w, r, sessionID)
	if !ok {
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

	decision, err := g.router.Route(r.Context(), router.Request{
		TaskType:       inferTaskType(req.Messages),
		Classification: session.Classification,
		PreferredAlias: req.Model,
	})
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}

	// Build upstream request.
	upstream := openrouter.ChatCompletionRequest{
		Provider:    decision.Provider,
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

	resp, err := g.backend.ChatCompletion(r.Context(), upstream)
	if err != nil {
		g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_call", body, nil, nil, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}

	respBody, _ := json.Marshal(resp)
	g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_call", body, respBody, &resp.Usage, "")

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
	reqHash := sha256Hex(reqBody)
	upstream.Stream = true
	upstream.StreamOptions = &openrouter.StreamOptions{IncludeUsage: true}
	streamReader, err := g.backend.ChatCompletionStream(r.Context(), upstream)
	if err != nil {
		g.auditModelCallHashes(r.Context(), sessionID, decision, "model.gateway_stream.failed", reqHash, "", err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("stream start failed: %v", err)})
		return
	}
	defer streamReader.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		g.auditModelCallHashes(r.Context(), sessionID, decision, "model.gateway_stream.failed", reqHash, "", "streaming not supported")
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	scanner := bufio.NewScanner(streamReader)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)
	responseHash := sha256.New()
	done := false
	var streamUsage *openrouter.Usage
	for scanner.Scan() {
		select {
		case <-r.Context().Done():
			g.auditModelCallHashes(context.Background(), sessionID, decision, "model.gateway_stream.failed", reqHash, "", r.Context().Err().Error())
			return
		default:
		}
		line := scanner.Text()
		if line == "" {
			continue
		}
		chunk, err := openrouter.DecodeStreamChunk(line)
		if err != nil {
			if errors.Is(err, io.EOF) {
				frame := "data: [DONE]\n\n"
				_, _ = responseHash.Write([]byte(frame))
				fmt.Fprint(w, frame)
				flusher.Flush()
				done = true
				break
			}
			continue // skip malformed lines
		}
		if usageHasValues(chunk.Usage) {
			streamUsage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			if chunk.Usage != nil {
				openAIChunk := openAIStreamChunk{
					ID:      chunk.ID,
					Object:  "chat.completion.chunk",
					Model:   decision.SelectedAlias,
					Choices: []openAIStreamChoice{},
					Usage:   openAIUsageFromOpenRouter(chunk.Usage),
				}
				data, _ := json.Marshal(openAIChunk)
				frame := fmt.Sprintf("data: %s\n\n", data)
				_, _ = responseHash.Write([]byte(frame))
				fmt.Fprint(w, frame)
				flusher.Flush()
			}
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
			Usage: openAIUsageFromOpenRouter(chunk.Usage),
		}

		data, _ := json.Marshal(openAIChunk)
		frame := fmt.Sprintf("data: %s\n\n", data)
		_, _ = responseHash.Write([]byte(frame))
		fmt.Fprint(w, frame)
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil {
		g.auditModelCallHashes(r.Context(), sessionID, decision, "model.gateway_stream.failed", reqHash, "", err.Error())
		return
	}
	if !done {
		g.auditModelCallHashes(r.Context(), sessionID, decision, "model.gateway_stream.failed", reqHash, "", "stream ended before done")
		return
	}
	respHash := "sha256:" + hex.EncodeToString(responseHash.Sum(nil))
	g.auditModelCallHashesWithUsage(r.Context(), sessionID, decision, "model.gateway_stream.completed", reqHash, respHash, openrouterUsageMap(streamUsage), "")
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
	if g.router == nil || g.backend == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "gateway unavailable"})
		return
	}
	sessionID, ok := requiredSessionID(w, r)
	if !ok {
		return
	}
	session, ok := g.sessionInfo(w, r, sessionID)
	if !ok {
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

	decision, err := g.router.Route(r.Context(), router.Request{
		TaskType:       inferTaskTypeFromInput(req.Input),
		Classification: session.Classification,
		PreferredAlias: req.Model,
	})
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}

	upstream := openrouter.ChatCompletionRequest{
		Provider:   decision.Provider,
		ModelAlias: decision.SelectedAlias,
		Model:      decision.SelectedModelID,
		Messages:   convertResponsesInput(req.Input),
	}

	resp, err := g.backend.ChatCompletion(r.Context(), upstream)
	if err != nil {
		g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_responses", body, nil, nil, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}

	respBody, _ := json.Marshal(resp)
	g.auditModelCall(r.Context(), sessionID, decision, "model.gateway_responses", body, respBody, &resp.Usage, "")

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

func (g *Gateway) sessionInfo(w http.ResponseWriter, r *http.Request, sessionID string) (SessionInfo, bool) {
	if g.lookupSession != nil {
		info, err := g.lookupSession(r.Context(), sessionID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return SessionInfo{}, false
		}
		if strings.TrimSpace(info.Classification) == "" {
			info.Classification = "internal"
		}
		return info, true
	}
	if g.validateSession != nil {
		if err := g.validateSession(r.Context(), sessionID); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return SessionInfo{}, false
		}
	}
	return SessionInfo{Classification: "internal"}, true
}

func randomID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_fallback_" + strconv.FormatUint(fallbackIDCounter.Add(1), 16)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

var fallbackIDCounter atomic.Uint64

// authorized delegates to httpauth.AuthorizedBearer so the constant-time bearer
// comparison has a single implementation shared across every endpoint. An empty
// runtime token always fails closed.
func (g *Gateway) authorized(r *http.Request) bool {
	return httpauth.AuthorizedBearer(r.Header.Get("Authorization"), g.runtimeToken)
}

func (g *Gateway) auditModelCall(ctx context.Context, sessionID string, decision router.Decision, eventType string, reqBody, respBody []byte, usage *openrouter.Usage, errMsg string) {
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
	g.auditModelCallHashesWithUsage(ctx, sessionID, decision, eventType, reqHash, respHash, openrouterUsageMap(usage), errMsg)
}

func (g *Gateway) auditModelCallHashes(ctx context.Context, sessionID string, decision router.Decision, eventType string, reqHash, respHash, errMsg string) {
	g.auditModelCallHashesWithUsage(ctx, sessionID, decision, eventType, reqHash, respHash, nil, errMsg)
}

func (g *Gateway) auditModelCallHashesWithUsage(ctx context.Context, sessionID string, decision router.Decision, eventType string, reqHash, respHash string, usage map[string]any, errMsg string) {
	if g.audit == nil {
		return
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
		ModelResolved:      g.resolvedModel(decision),
		RequestSHA256:      reqHash,
		ResponseSHA256:     respHash,
		TokenUsage:         usage,
		GatewayBackend:     g.backendName(),
		TrustLevel:         "gateway_enforced",
		EnforcementMode:    "gateway",
		Reason:             reason,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "model-gateway",
	})
}

func openrouterUsageMap(usage *openrouter.Usage) map[string]any {
	if usage == nil {
		return nil
	}
	return map[string]any{
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
		"reasoning_tokens":  usage.CompletionTokensDetails.ReasoningTokens,
		"cost_usd":          usage.Cost,
	}
}

func usageHasValues(usage *openrouter.Usage) bool {
	if usage == nil {
		return false
	}
	return usage.PromptTokens > 0 ||
		usage.CompletionTokens > 0 ||
		usage.TotalTokens > 0 ||
		usage.Cost > 0 ||
		usage.CompletionTokensDetails.ReasoningTokens > 0
}

func openAIUsageFromOpenRouter(usage *openrouter.Usage) *openAIUsage {
	if usage == nil {
		return nil
	}
	return &openAIUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
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
	return inferTaskTypeFromText(messages[len(messages)-1].Content)
}

func inferTaskTypeFromInput(input []openAIResponsesInput) string {
	if len(input) == 0 {
		return "general"
	}
	return inferTaskTypeFromText(input[len(input)-1].Content)
}

func inferTaskTypeFromText(text string) string {
	content := strings.ToLower(text)
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

func (g *Gateway) backendName() string {
	if g == nil || g.backend == nil {
		return ""
	}
	return g.backend.Name()
}

func (g *Gateway) resolvedModel(decision router.Decision) string {
	if g == nil || g.backend == nil {
		return decision.SelectedModelID
	}
	return g.backend.ResolvedModel(decision.Provider, decision.SelectedModelID)
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
	Usage   *openAIUsage         `json:"usage,omitempty"`
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
