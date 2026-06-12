// Package modelgateway implements the OpenAI-compatible Model Compatibility Gateway.
// It exposes governed model aliases to runtimes without handing them provider keys.
package modelgateway

import (
	"bufio"
	"bytes"
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

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/copilot"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/modelbackend"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

// GatewayConfig holds the configuration for the model compatibility gateway.
type GatewayConfig struct {
	RuntimeToken      string
	Router            *router.Router
	Backend           modelbackend.Backend
	Audit             audit.Store
	NewID             func(prefix string) string
	ValidateSession   func(context.Context, string) error
	LookupSession     func(context.Context, string) (SessionInfo, error)
	AutoSession       func(context.Context, AutoSessionRequest) (SessionInfo, error)
	FinishAutoSession func(context.Context, string, string) error
	DelegateTask      func(context.Context, TaskDelegationRequest) error
	// MaxRequestBytes caps inbound request bodies. Coding agents send
	// multi-megabyte contexts, so the default is deliberately generous.
	MaxRequestBytes int64
}

type TaskDelegationRequest struct {
	ParentSessionID     string
	ActorSubject        string
	ParentAgent         string
	Agent               string
	Description         string
	Prompt              string
	ModelAlias          string
	RequestedModelAlias string
	Provider            string
	ToolCallID          string
	SourceSystem        string
}

type AutoSessionRequest struct {
	ActorSubject       string
	ModelAlias         string
	PromptSHA256       string
	Client             string
	Endpoint           string
	Classification     string
	RawRequestBody     []byte
	TrustedClientToken string
	UseCaseID          string
	WorkflowID         string
	WorkItemID         string
	WorkItemType       string
	RepoURL            string
	Branch             string
	CommitSHA          string
	Intent             string
	ActorHint          string
	SourceSystem       string
	EstimatedCostUSD   float64
}

const defaultMaxRequestBytes = 20 << 20 // 20 MiB

// SessionInfo is server-side session context used by runtime model routing.
type SessionInfo struct {
	SessionID      string
	Classification string
	Status         string
	// GatewayTokenSHA256, when set, requires callers to present the matching
	// per-session secret via X-AI-Orch-Session-Token. The runtime variant is
	// minted at dispatch for server-side runtimes; either hash is accepted.
	GatewayTokenSHA256        string
	RuntimeGatewayTokenSHA256 string
	ActorSubject              string
	Agent                     string
	RunID                     string
	UseCaseID                 string
	WorkflowID                string
	WorkItemID                string
	WorkItemType              string
	RepoURL                   string
	Branch                    string
	CommitSHA                 string
	ActorHint                 string
	SourceSystem              string
	PermissionMode            string
	ApprovalMode              string
	WorkspaceMode             string
	GatewayToken              string
	AutoCreated               bool
}

// Gateway is an OpenAI-compatible model endpoint owned by the Governance Shell.
type Gateway struct {
	runtimeToken      string
	router            *router.Router
	backend           modelbackend.Backend
	audit             audit.Store
	newID             func(prefix string) string
	validateSession   func(context.Context, string) error
	lookupSession     func(context.Context, string) (SessionInfo, error)
	autoSession       func(context.Context, AutoSessionRequest) (SessionInfo, error)
	finishAutoSession func(context.Context, string, string) error
	delegateTask      func(context.Context, TaskDelegationRequest) error
	maxRequestBytes   int64
}

// NewGateway creates a new model compatibility gateway.
func NewGateway(cfg GatewayConfig) *Gateway {
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	maxRequestBytes := cfg.MaxRequestBytes
	if maxRequestBytes <= 0 {
		maxRequestBytes = defaultMaxRequestBytes
	}
	return &Gateway{
		runtimeToken:      cfg.RuntimeToken,
		router:            cfg.Router,
		backend:           cfg.Backend,
		audit:             cfg.Audit,
		newID:             newID,
		validateSession:   cfg.ValidateSession,
		lookupSession:     cfg.LookupSession,
		autoSession:       cfg.AutoSession,
		finishAutoSession: cfg.FinishAutoSession,
		delegateTask:      cfg.DelegateTask,
		maxRequestBytes:   maxRequestBytes,
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
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !g.authorized(r) {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if g.router == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "router unavailable"})
		return
	}

	// Prefer the governed session context when present; otherwise use the query,
	// headers, and composite API key so generic provider UIs can still receive
	// actor-bound Copilot picker models.
	classification, actor := g.modelListContext(r)

	models := g.router.AvailableAliases(r.Context(), router.Request{Classification: classification, ActorSubject: actor})
	data := make([]modelListEntry, 0, len(models))
	for _, m := range models {
		data = append(data, modelListEntry{
			ID:      m.Alias,
			Object:  "model",
			OwnedBy: "ai-orch",
		})
	}
	data = g.appendDynamicCopilotModelEntries(r.Context(), actor, classification, data)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

type modelListEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
	Name    string `json:"name,omitempty"`
}

func (g *Gateway) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !g.authorized(r) {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if g.router == nil || g.backend == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "gateway unavailable"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, g.maxRequestBytes))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "read body: " + err.Error()})
		return
	}

	var req openAIChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Model == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "model is required"})
		return
	}
	if len(req.Messages) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "messages are required"})
		return
	}
	sessionID, session, ok := g.resolveSession(w, r, req.Model, body, "chat.completions")
	if !ok {
		return
	}
	finishStatus := "failed"
	defer func() {
		if finishStatus != "" {
			g.finishGatewayAutoSession(context.Background(), sessionID, session, finishStatus)
		}
	}()

	decision, err := g.routeModel(r.Context(), req.Model, session, inferTaskType(req.Messages))
	if err != nil {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}
	body, decision, err = applyGovernedReasoning(body, decision, session)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if req.Stream {
		finishStatus = ""
		g.handleStream(w, r, req, decision, session, sessionID, body)
		return
	}

	if rawBackend, ok := g.backend.(modelbackend.RawChatBackend); ok {
		respBody, err := rawBackend.ChatCompletionRaw(r.Context(), modelbackend.RawRequest{
			Provider:     decision.Provider,
			ModelAlias:   decision.SelectedAlias,
			Model:        decision.SelectedModelID,
			Body:         body,
			ActorSubject: session.ActorSubject,
		})
		if err != nil {
			g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_call", body, nil, nil, err.Error())
			httpx.WriteJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
			return
		}
		respBody = rewriteTopLevelModel(respBody, decision.SelectedAlias)
		usage := usageFromRawResponse(respBody)
		g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_call", body, respBody, usage, "")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBody)
		return
	}

	upstream := openrouter.ChatCompletionRequest{
		Provider:    decision.Provider,
		ModelAlias:  decision.SelectedAlias,
		Model:       decision.SelectedModelID,
		Messages:    convertMessages(req.Messages),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	if decision.ReasoningSupportsEffort && decision.ReasoningEffortApplied != "" {
		upstream.Reasoning = &openrouter.ReasoningConfig{Effort: decision.ReasoningEffortApplied}
	}

	resp, err := g.backend.ChatCompletion(r.Context(), upstream)
	if err != nil {
		g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_call", body, nil, nil, err.Error())
		httpx.WriteJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}

	respBody, _ := json.Marshal(resp)
	g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_call", body, respBody, openrouterUsageMap(&resp.Usage), "")
	finishStatus = "completed"

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

	httpx.WriteJSON(w, http.StatusOK, openAIResp)
}

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request, req openAIChatCompletionRequest, decision router.Decision, session SessionInfo, sessionID string, reqBody []byte) {
	finishStatus := "failed"
	defer func() {
		g.finishGatewayAutoSession(context.Background(), sessionID, session, finishStatus)
	}()
	reqHash := sha256Hex(reqBody)
	streamBody := ensureChatStreamOptions(reqBody)
	streamReader, err := startChatStream(r.Context(), g.backend, decision, req, session.ActorSubject, streamBody)
	if err != nil {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", err.Error())
		httpx.WriteJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("stream start failed: %v", err)})
		return
	}
	defer streamReader.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", "streaming not supported")
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
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
	var streamUsage map[string]any
	toolCalls := newToolCallAuditTracker()
	for scanner.Scan() {
		select {
		case <-r.Context().Done():
			g.auditModelCallHashesWithUsageAndTools(context.Background(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", nil, toolCalls.Summary(), r.Context().Err().Error())
			return
		default:
		}
		line := scanner.Text()
		if line == "" {
			_, _ = responseHash.Write([]byte("\n"))
			fmt.Fprint(w, "\n")
			flusher.Flush()
			continue
		}
		line, lineDone, usage := rewriteSSELine(line, decision.SelectedAlias)
		if lineDone {
			done = true
		}
		if usage != nil {
			streamUsage = usage
		}
		toolCalls.ObserveChatCompletionSSELine(line)
		frame := line + "\n"
		_, _ = responseHash.Write([]byte(frame))
		fmt.Fprint(w, frame)
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil {
		g.auditModelCallHashesWithUsageAndTools(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", nil, toolCalls.Summary(), err.Error())
		return
	}
	if !done {
		g.auditModelCallHashesWithUsageAndTools(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", nil, toolCalls.Summary(), "stream ended before done")
		return
	}
	respHash := "sha256:" + hex.EncodeToString(responseHash.Sum(nil))
	g.auditModelCallHashesWithUsageAndTools(r.Context(), sessionID, session, decision, "model.gateway_stream.completed", reqHash, respHash, streamUsage, toolCalls.Summary(), "")
	finishStatus = "completed"
}

func (g *Gateway) finishGatewayAutoSession(ctx context.Context, sessionID string, session SessionInfo, status string) {
	if g == nil || g.finishAutoSession == nil || !session.AutoCreated || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(status) == "" {
		return
	}
	_ = g.finishAutoSession(ctx, sessionID, status)
}

func randomID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_fallback_" + strconv.FormatUint(fallbackIDCounter.Add(1), 16)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

var fallbackIDCounter atomic.Uint64

// providerErrorStatus distinguishes caller-fixable failures from upstream
// outages so runtimes see an actionable status instead of a blanket 502.
// A missing or revoked Copilot enrollment is the caller's problem to fix.
func providerErrorStatus(err error) int {
	if errors.Is(err, copilot.ErrTokenNotFound) || errors.Is(err, copilot.ErrTokenRevoked) {
		return http.StatusForbidden
	}
	return http.StatusBadGateway
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

// OpenAI-compatible request/response types.

func (c *rawTextContent) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		*c = ""
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		*c = rawTextContent(text)
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(trimmed, &parts); err == nil {
		var texts []string
		for _, part := range parts {
			if part.Type == "text" || part.Type == "input_text" {
				texts = append(texts, part.Text)
			}
		}
		*c = rawTextContent(strings.Join(texts, "\n"))
		return nil
	}
	*c = ""
	return nil
}

func (c rawTextContent) String() string {
	return string(c)
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

// OpenAI Responses API types.

type openAIResponsesInputSet []openAIResponsesInput

func (s *openAIResponsesInputSet) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '"' {
		var input string
		if err := json.Unmarshal(trimmed, &input); err != nil {
			return err
		}
		*s = []openAIResponsesInput{{Role: "user", Content: rawTextContent(input)}}
		return nil
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawItems); err != nil {
		return err
	}
	items := make([]openAIResponsesInput, 0, len(rawItems))
	for _, raw := range rawItems {
		var item openAIResponsesInput
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		if item.Content.String() == "" {
			item.Content = rawTextContent(textFromResponsesItem(raw, item))
		}
		items = append(items, item)
	}
	*s = items
	return nil
}

func textFromResponsesItem(raw json.RawMessage, item openAIResponsesInput) string {
	if item.Output != "" {
		return item.Output
	}
	if item.Args != "" {
		return item.Args
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	contentRaw, ok := obj["content"]
	if !ok {
		return ""
	}
	var parts []map[string]any
	if err := json.Unmarshal(contentRaw, &parts); err != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		for _, key := range []string{"text", "output"} {
			if text, ok := part[key].(string); ok && text != "" {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "\n")
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
