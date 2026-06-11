// Package modelgateway implements the OpenAI-compatible Model Compatibility Gateway.
// It exposes governed model aliases to runtimes without handing them provider keys.
package modelgateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	"ai-agent-orch/internal/copilot"
	"ai-agent-orch/internal/modelbackend"
	"ai-agent-orch/internal/openrouter"
	"ai-agent-orch/internal/router"
)

// GatewayConfig holds the configuration for the model compatibility gateway.
type GatewayConfig struct {
	RuntimeToken    string
	Router          *router.Router
	Backend         modelbackend.Backend
	Audit           audit.Store
	NewID           func(prefix string) string
	ValidateSession func(context.Context, string) error
	LookupSession   func(context.Context, string) (SessionInfo, error)
	AutoSession     func(context.Context, AutoSessionRequest) (SessionInfo, error)
	// MaxRequestBytes caps inbound request bodies. Coding agents send
	// multi-megabyte contexts, so the default is deliberately generous.
	MaxRequestBytes int64
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
	autoSession     func(context.Context, AutoSessionRequest) (SessionInfo, error)
	maxRequestBytes int64
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
		runtimeToken:    cfg.RuntimeToken,
		router:          cfg.Router,
		backend:         cfg.Backend,
		audit:           cfg.Audit,
		newID:           newID,
		validateSession: cfg.ValidateSession,
		lookupSession:   cfg.LookupSession,
		autoSession:     cfg.AutoSession,
		maxRequestBytes: maxRequestBytes,
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

	// Prefer the governed session's classification when the runtime supplies a
	// session header; the query parameter remains a fallback for bare listings.
	classification := ""
	if sessionID := strings.TrimSpace(r.Header.Get("X-AI-Orch-Session-ID")); sessionID != "" && g.lookupSession != nil {
		if info, err := g.lookupSession(r.Context(), sessionID); err == nil && sessionTokenMatches(r, info.GatewayTokenSHA256, info.RuntimeGatewayTokenSHA256) {
			classification = strings.TrimSpace(info.Classification)
		}
	}
	if classification == "" {
		classification = r.URL.Query().Get("classification")
	}
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, g.maxRequestBytes))
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
	sessionID, session, ok := g.resolveSession(w, r, req.Model, body, "chat.completions")
	if !ok {
		return
	}

	decision, err := g.router.Route(r.Context(), router.Request{
		TaskType:       inferTaskType(req.Messages),
		Classification: session.Classification,
		PreferredAlias: req.Model,
		ActorSubject:   session.ActorSubject,
	})
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}
	body, decision, err = applyGovernedReasoning(body, decision, session)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if req.Stream {
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
			writeJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
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
		writeJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}

	respBody, _ := json.Marshal(resp)
	g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_call", body, respBody, openrouterUsageMap(&resp.Usage), "")

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

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request, req openAIChatCompletionRequest, decision router.Decision, session SessionInfo, sessionID string, reqBody []byte) {
	reqHash := sha256Hex(reqBody)
	streamBody := ensureChatStreamOptions(reqBody)
	streamReader, err := startChatStream(r.Context(), g.backend, decision, req, session.ActorSubject, streamBody)
	if err != nil {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", err.Error())
		writeJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("stream start failed: %v", err)})
		return
	}
	defer streamReader.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", "streaming not supported")
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
	var streamUsage map[string]any
	for scanner.Scan() {
		select {
		case <-r.Context().Done():
			g.auditModelCallHashes(context.Background(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", r.Context().Err().Error())
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
		frame := line + "\n"
		_, _ = responseHash.Write([]byte(frame))
		fmt.Fprint(w, frame)
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", err.Error())
		return
	}
	if !done {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", "stream ended before done")
		return
	}
	respHash := "sha256:" + hex.EncodeToString(responseHash.Sum(nil))
	g.auditModelCallHashesWithUsage(r.Context(), sessionID, session, decision, "model.gateway_stream.completed", reqHash, respHash, streamUsage, "")
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, g.maxRequestBytes))
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
	sessionID, session, ok := g.resolveSession(w, r, req.Model, body, "responses")
	if !ok {
		return
	}

	decision, err := g.router.Route(r.Context(), router.Request{
		TaskType:       inferTaskTypeFromInput(req.Input),
		Classification: session.Classification,
		PreferredAlias: req.Model,
		ActorSubject:   session.ActorSubject,
	})
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}
	body, decision, err = applyGovernedReasoning(body, decision, session)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if rawBackend, ok := g.backend.(modelbackend.RawResponsesBackend); ok {
		if req.Stream {
			g.handleResponsesStream(w, r, rawBackend, decision, session, sessionID, body)
			return
		}
		respBody, err := rawBackend.ResponsesRaw(r.Context(), modelbackend.RawRequest{
			Provider:     decision.Provider,
			ModelAlias:   decision.SelectedAlias,
			Model:        decision.SelectedModelID,
			Body:         body,
			ActorSubject: session.ActorSubject,
		})
		if err != nil {
			g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_responses", body, nil, nil, err.Error())
			writeJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
			return
		}
		respBody = rewriteTopLevelModel(respBody, decision.SelectedAlias)
		usage := usageFromRawResponse(respBody)
		g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_responses", body, respBody, usage, "")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBody)
		return
	}

	if req.Stream {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "responses streaming is not supported by the selected backend"})
		return
	}

	upstream := openrouter.ChatCompletionRequest{
		Provider:   decision.Provider,
		ModelAlias: decision.SelectedAlias,
		Model:      decision.SelectedModelID,
		Messages:   convertResponsesInput(req.Input),
	}
	if decision.ReasoningSupportsEffort && decision.ReasoningEffortApplied != "" {
		upstream.Reasoning = &openrouter.ReasoningConfig{Effort: decision.ReasoningEffortApplied}
	}

	resp, err := g.backend.ChatCompletion(r.Context(), upstream)
	if err != nil {
		g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_responses", body, nil, nil, err.Error())
		writeJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}

	respBody, _ := json.Marshal(resp)
	g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_responses", body, respBody, openrouterUsageMap(&resp.Usage), "")

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

func (g *Gateway) handleResponsesStream(w http.ResponseWriter, r *http.Request, backend modelbackend.RawResponsesBackend, decision router.Decision, session SessionInfo, sessionID string, reqBody []byte) {
	reqHash := sha256Hex(reqBody)
	streamBody := ensureJSONBool(reqBody, "stream", true)
	streamReader, err := backend.ResponsesStreamRaw(r.Context(), modelbackend.RawRequest{
		Provider:     decision.Provider,
		ModelAlias:   decision.SelectedAlias,
		Model:        decision.SelectedModelID,
		Body:         streamBody,
		ActorSubject: session.ActorSubject,
	})
	if err != nil {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.failed", reqHash, "", err.Error())
		writeJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("responses stream start failed: %v", err)})
		return
	}
	defer streamReader.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.failed", reqHash, "", "streaming not supported")
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
	var streamUsage map[string]any
	for scanner.Scan() {
		select {
		case <-r.Context().Done():
			g.auditModelCallHashes(context.Background(), sessionID, session, decision, "model.gateway_responses_stream.failed", reqHash, "", r.Context().Err().Error())
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
		if lineDone || strings.Contains(line, "response.completed") || strings.Contains(line, "response.incomplete") {
			done = true
		}
		if usage != nil {
			streamUsage = usage
		}
		frame := line + "\n"
		_, _ = responseHash.Write([]byte(frame))
		fmt.Fprint(w, frame)
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.failed", reqHash, "", err.Error())
		return
	}
	if !done {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.failed", reqHash, "", "stream ended before completion")
		return
	}
	respHash := "sha256:" + hex.EncodeToString(responseHash.Sum(nil))
	g.auditModelCallHashesWithUsage(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.completed", reqHash, respHash, streamUsage, "")
}

func (g *Gateway) resolveSession(w http.ResponseWriter, r *http.Request, modelAlias string, body []byte, endpoint string) (string, SessionInfo, bool) {
	if sessionID := strings.TrimSpace(r.Header.Get("X-AI-Orch-Session-ID")); sessionID != "" {
		info, ok := g.sessionInfo(w, r, sessionID)
		return sessionID, info, ok
	}
	if g.autoSession == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "X-AI-Orch-Session-ID header is required"})
		return "", SessionInfo{}, false
	}
	actor := strings.TrimSpace(r.Header.Get("X-AI-Orch-Actor-Subject"))
	if actor == "" {
		actor = strings.TrimSpace(r.Header.Get("X-AI-Orch-Local-Identity"))
	}
	if actor == "" {
		// Composite API key "<runtime-token>.<actor>" carries identity for
		// clients that cannot send custom headers.
		actor, _ = g.runtimeAuth(r)
	}
	if actor == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "actor identity is required for auto sessions: send X-AI-Orch-Actor-Subject or use the composite API key <runtime-token>.<actor>"})
		return "", SessionInfo{}, false
	}
	classification := strings.TrimSpace(r.Header.Get("X-AI-Orch-Classification"))
	if classification == "" {
		classification = "internal"
	}
	info, err := g.autoSession(r.Context(), AutoSessionRequest{
		ActorSubject:       actor,
		ModelAlias:         modelAlias,
		PromptSHA256:       sha256Hex(body),
		Client:             strings.TrimSpace(r.Header.Get("X-AI-Orch-Client")),
		Endpoint:           endpoint,
		Classification:     classification,
		RawRequestBody:     body,
		TrustedClientToken: strings.TrimSpace(r.Header.Get("X-AI-Orch-Trusted-Client-Token")),
		UseCaseID:          strings.TrimSpace(r.Header.Get("X-AI-Orch-Use-Case-ID")),
		WorkflowID:         strings.TrimSpace(r.Header.Get("X-AI-Orch-Workflow-ID")),
		WorkItemID:         strings.TrimSpace(r.Header.Get("X-AI-Orch-Work-Item-ID")),
		WorkItemType:       strings.TrimSpace(r.Header.Get("X-AI-Orch-Work-Item-Type")),
		RepoURL:            strings.TrimSpace(r.Header.Get("X-AI-Orch-Repo-URL")),
		Branch:             strings.TrimSpace(r.Header.Get("X-AI-Orch-Branch")),
		CommitSHA:          strings.TrimSpace(r.Header.Get("X-AI-Orch-Commit-SHA")),
		Intent:             strings.TrimSpace(r.Header.Get("X-AI-Orch-Intent")),
		ActorHint:          strings.TrimSpace(r.Header.Get("X-AI-Orch-Actor-Hint")),
		SourceSystem:       strings.TrimSpace(r.Header.Get("X-AI-Orch-Source-System")),
		EstimatedCostUSD:   parseOptionalFloatHeader(r.Header.Get("X-AI-Orch-Estimated-Cost-USD")),
	})
	if err != nil {
		writeJSON(w, statusCodeFromAutoSessionError(err), map[string]any{"error": err.Error()})
		return "", SessionInfo{}, false
	}
	if info.SessionID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "auto session missing session ID"})
		return "", SessionInfo{}, false
	}
	if strings.TrimSpace(info.Classification) == "" {
		info.Classification = "internal"
	}
	if strings.TrimSpace(info.ActorSubject) == "" {
		info.ActorSubject = actor
	}
	w.Header().Set("X-AI-Orch-Session-ID", info.SessionID)
	if strings.TrimSpace(info.GatewayToken) != "" {
		w.Header().Set("X-AI-Orch-Session-Token", info.GatewayToken)
	}
	return info.SessionID, info, true
}

func (g *Gateway) sessionInfo(w http.ResponseWriter, r *http.Request, sessionID string) (SessionInfo, bool) {
	if g.lookupSession != nil {
		info, err := g.lookupSession(r.Context(), sessionID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return SessionInfo{}, false
		}
		if !sessionTokenMatches(r, info.GatewayTokenSHA256, info.RuntimeGatewayTokenSHA256) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "X-AI-Orch-Session-Token missing or invalid for this session"})
			return SessionInfo{}, false
		}
		if !sessionStatusAllowsModelCalls(info.Status) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "session is not active for model calls"})
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

func sessionStatusAllowsModelCalls(status string) bool {
	switch strings.TrimSpace(status) {
	case "", "created", "awaiting_confirmation", "confirming", "confirmed", "running":
		return true
	default:
		return false
	}
}

func statusCodeFromAutoSessionError(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "kill switch"):
		return http.StatusLocked
	case strings.Contains(message, "work item") || strings.Contains(message, "feature branch") || strings.Contains(message, "protected branch"):
		return http.StatusPreconditionRequired
	case strings.Contains(message, "cost cap"):
		return http.StatusPaymentRequired
	case strings.Contains(message, "secret detected") || strings.Contains(message, "classification") || strings.Contains(message, "policy denied"):
		return http.StatusForbidden
	case strings.Contains(message, "valid actor"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func parseOptionalFloatHeader(value string) float64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

// sessionTokenMatches verifies the per-session gateway secret against any of
// the stored hashes (client token, dispatch-time runtime token). Sessions
// with no stored hash predate token binding and pass unchanged.
func sessionTokenMatches(r *http.Request, wantHashesHex ...string) bool {
	anyConfigured := false
	presented := strings.TrimSpace(r.Header.Get("X-AI-Orch-Session-Token"))
	var sum [sha256.Size]byte
	if presented != "" {
		sum = sha256.Sum256([]byte(presented))
	}
	for _, wantHashHex := range wantHashesHex {
		wantHashHex = strings.TrimSpace(wantHashHex)
		if wantHashHex == "" {
			continue
		}
		anyConfigured = true
		if presented == "" {
			continue
		}
		got, err := hex.DecodeString(wantHashHex)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare(sum[:], got) == 1 {
			return true
		}
	}
	return !anyConfigured
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
	_, ok := g.runtimeAuth(r)
	return ok
}

// runtimeAuth validates the runtime bearer and extracts the optional actor
// suffix from a composite key "<runtime-token>.<actor>". The composite form
// exists for OpenAI-compatible clients that cannot send custom headers (for
// example Cline), so identity can travel in the only field every client
// reliably forwards. The plain runtime token remains valid.
func (g *Gateway) runtimeAuth(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if g.runtimeToken == "" || !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(header, prefix)
	if subtle.ConstantTimeCompare([]byte(token), []byte(g.runtimeToken)) == 1 {
		return "", true
	}
	sep := g.runtimeToken + "."
	if len(token) > len(sep) && subtle.ConstantTimeCompare([]byte(token[:len(sep)]), []byte(sep)) == 1 {
		actor := strings.TrimSpace(token[len(sep):])
		if compositeActorLabelOK(actor) {
			return actor, true
		}
	}
	return "", false
}

// compositeActorLabelOK keeps API-key-borne identities to safe label
// characters so they cannot smuggle header or log injection payloads.
func compositeActorLabelOK(actor string) bool {
	if actor == "" || len(actor) > 64 {
		return false
	}
	for _, r := range actor {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == '@':
		default:
			return false
		}
	}
	return true
}

// providerErrorStatus distinguishes caller-fixable failures from upstream
// outages so runtimes see an actionable status instead of a blanket 502.
// A missing or revoked Copilot enrollment is the caller's problem to fix.
func providerErrorStatus(err error) int {
	if errors.Is(err, copilot.ErrTokenNotFound) || errors.Is(err, copilot.ErrTokenRevoked) {
		return http.StatusForbidden
	}
	return http.StatusBadGateway
}

func applyGovernedReasoning(body []byte, decision router.Decision, session SessionInfo) ([]byte, router.Decision, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, decision, fmt.Errorf("decode request for reasoning policy: %w", err)
	}
	requested, requestedPresent, err := extractReasoningEffort(obj)
	if err != nil {
		return nil, decision, err
	}
	applied, source := chooseReasoningEffort(requested, requestedPresent, decision, session)
	decision.ReasoningEffortRequested = requested
	decision.ReasoningEffortApplied = applied
	decision.ReasoningSource = source
	encoded, err := rewriteReasoningEffort(obj, decision.ReasoningSupportsEffort, applied)
	if err != nil {
		return nil, decision, err
	}
	return encoded, decision, nil
}

func extractReasoningEffort(obj map[string]json.RawMessage) (string, bool, error) {
	for _, key := range []string{"reasoningEffort", "reasoning_effort"} {
		if raw, ok := obj[key]; ok {
			effort, err := reasoningEffortFromRaw(raw)
			if err != nil {
				return "", true, fmt.Errorf("%s must be one of low, medium, high", key)
			}
			return effort, true, nil
		}
	}
	if raw, ok := obj["reasoning"]; ok {
		var reasoning map[string]json.RawMessage
		if err := json.Unmarshal(raw, &reasoning); err == nil {
			if effortRaw, ok := reasoning["effort"]; ok {
				effort, err := reasoningEffortFromRaw(effortRaw)
				if err != nil {
					return "", true, errors.New("reasoning.effort must be one of low, medium, high")
				}
				return effort, true, nil
			}
		}
	}
	return "", false, nil
}

func reasoningEffortFromRaw(raw json.RawMessage) (string, error) {
	var effort string
	if err := json.Unmarshal(raw, &effort); err != nil {
		return "", err
	}
	effort = normalizeReasoningEffort(effort)
	if effort == "" {
		return "", errors.New("invalid reasoning effort")
	}
	return effort, nil
}

func chooseReasoningEffort(requested string, requestedPresent bool, decision router.Decision, session SessionInfo) (string, string) {
	agentDefault, agentMax := agentReasoningPolicy(session.Agent)
	if !decision.ReasoningSupportsEffort {
		if requestedPresent || agentDefault != "" || decision.ReasoningDefaultEffort != "" {
			return "", "provider_default"
		}
		return "", ""
	}
	applied := ""
	source := ""
	if requestedPresent {
		applied = requested
		source = "client"
	} else if agentDefault != "" {
		applied = agentDefault
		source = "agent_default"
	} else if decision.ReasoningDefaultEffort != "" {
		applied = decision.ReasoningDefaultEffort
		source = "route_default"
	}
	maxEffort := stricterReasoningMax(decision.ReasoningMaxEffort, agentMax)
	if applied != "" && maxEffort != "" && reasoningRank(applied) > reasoningRank(maxEffort) {
		applied = maxEffort
		source = "policy_clamped"
	}
	return applied, source
}

func rewriteReasoningEffort(obj map[string]json.RawMessage, supportsEffort bool, applied string) ([]byte, error) {
	delete(obj, "reasoningEffort")
	delete(obj, "reasoning_effort")
	if !supportsEffort {
		delete(obj, "reasoning")
		return json.Marshal(obj)
	}
	if applied == "" {
		return json.Marshal(obj)
	}
	reasoning := map[string]json.RawMessage{}
	if raw, ok := obj["reasoning"]; ok {
		_ = json.Unmarshal(raw, &reasoning)
	}
	effortJSON, err := json.Marshal(applied)
	if err != nil {
		return nil, fmt.Errorf("encode reasoning effort: %w", err)
	}
	reasoning["effort"] = effortJSON
	reasoningJSON, err := json.Marshal(reasoning)
	if err != nil {
		return nil, fmt.Errorf("encode reasoning object: %w", err)
	}
	obj["reasoning"] = reasoningJSON
	return json.Marshal(obj)
}

func agentReasoningPolicy(agent string) (defaultEffort string, maxEffort string) {
	switch strings.TrimSpace(agent) {
	case "governance-lead":
		return "low", "medium"
	case "code-review", "unit-tests", "backend-development", "frontend-development", "documentation", "refactor":
		return "medium", "high"
	case "security-review", "security-scan", "architecture-review", "terraform-review":
		return "medium", "high"
	default:
		return "", ""
	}
}

func stricterReasoningMax(routeMax string, agentMax string) string {
	routeMax = normalizeReasoningEffort(routeMax)
	agentMax = normalizeReasoningEffort(agentMax)
	if routeMax == "" {
		return agentMax
	}
	if agentMax == "" {
		return routeMax
	}
	if reasoningRank(agentMax) < reasoningRank(routeMax) {
		return agentMax
	}
	return routeMax
}

func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return ""
	}
}

func reasoningRank(effort string) int {
	switch normalizeReasoningEffort(effort) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	default:
		return 0
	}
}

func (g *Gateway) auditModelCall(ctx context.Context, sessionID string, session SessionInfo, decision router.Decision, eventType string, reqBody, respBody []byte, usage map[string]any, errMsg string) {
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
	g.auditModelCallHashesWithUsage(ctx, sessionID, session, decision, eventType, reqHash, respHash, usage, errMsg)
}

func (g *Gateway) auditModelCallHashes(ctx context.Context, sessionID string, session SessionInfo, decision router.Decision, eventType string, reqHash, respHash, errMsg string) {
	g.auditModelCallHashesWithUsage(ctx, sessionID, session, decision, eventType, reqHash, respHash, nil, errMsg)
}

func (g *Gateway) auditModelCallHashesWithUsage(ctx context.Context, sessionID string, session SessionInfo, decision router.Decision, eventType string, reqHash, respHash string, usage map[string]any, errMsg string) {
	if g.audit == nil {
		return
	}
	reason := ""
	if errMsg != "" {
		reason = errMsg
	}
	actor := strings.TrimSpace(session.ActorSubject)
	if actor == "" {
		actor = "runtime"
	}
	_, _ = g.audit.Append(ctx, audit.Event{
		EventID:                  g.newID("evt"),
		SessionID:                sessionID,
		EventType:                eventType,
		Actor:                    actor,
		Agent:                    session.Agent,
		Classification:           session.Classification,
		Provider:                 decision.Provider,
		ModelAlias:               decision.SelectedAlias,
		ModelResolved:            g.resolvedModel(decision),
		RequestedModelAlias:      decision.RequestedAlias,
		CredentialSource:         decision.CredentialSource,
		ReasoningEffortRequested: decision.ReasoningEffortRequested,
		ReasoningEffortApplied:   decision.ReasoningEffortApplied,
		ReasoningSource:          decision.ReasoningSource,
		RequestSHA256:            reqHash,
		ResponseSHA256:           respHash,
		TokenUsage:               usage,
		GatewayBackend:           g.backendName(),
		RunID:                    session.RunID,
		PermissionMode:           session.PermissionMode,
		ApprovalMode:             session.ApprovalMode,
		WorkspaceMode:            session.WorkspaceMode,
		WorkItemID:               session.WorkItemID,
		WorkItemType:             session.WorkItemType,
		CommitSHA:                session.CommitSHA,
		ActorHint:                session.ActorHint,
		SourceSystem:             session.SourceSystem,
		TrustLevel:               "gateway_enforced",
		EnforcementMode:          "gateway",
		Reason:                   reason,
		RawPromptStored:          false,
		RawResponseStored:        false,
		CorrelationSubject:       "model-gateway",
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

func startChatStream(ctx context.Context, backend modelbackend.Backend, decision router.Decision, req openAIChatCompletionRequest, actorSubject string, body []byte) (io.ReadCloser, error) {
	if rawBackend, ok := backend.(modelbackend.RawChatBackend); ok {
		return rawBackend.ChatCompletionStreamRaw(ctx, modelbackend.RawRequest{
			Provider:     decision.Provider,
			ModelAlias:   decision.SelectedAlias,
			Model:        decision.SelectedModelID,
			Body:         body,
			ActorSubject: actorSubject,
		})
	}
	upstream := openrouter.ChatCompletionRequest{
		Provider:      decision.Provider,
		ModelAlias:    decision.SelectedAlias,
		Model:         decision.SelectedModelID,
		Messages:      convertMessages(req.Messages),
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		Stream:        true,
		StreamOptions: &openrouter.StreamOptions{IncludeUsage: true},
	}
	if decision.ReasoningSupportsEffort && decision.ReasoningEffortApplied != "" {
		upstream.Reasoning = &openrouter.ReasoningConfig{Effort: decision.ReasoningEffortApplied}
	}
	return backend.ChatCompletionStream(ctx, upstream)
}

func ensureChatStreamOptions(body []byte) []byte {
	body = ensureJSONBool(body, "stream", true)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	if _, ok := obj["stream_options"]; !ok {
		obj["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	} else {
		var opts map[string]json.RawMessage
		if err := json.Unmarshal(obj["stream_options"], &opts); err == nil {
			opts["include_usage"] = json.RawMessage(`true`)
			if encoded, err := json.Marshal(opts); err == nil {
				obj["stream_options"] = encoded
			}
		}
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return encoded
}

func ensureJSONBool(body []byte, key string, value bool) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	if value {
		obj[key] = json.RawMessage(`true`)
	} else {
		obj[key] = json.RawMessage(`false`)
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return encoded
}

func rewriteTopLevelModel(body []byte, alias string) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	model, err := json.Marshal(alias)
	if err != nil {
		return body
	}
	obj["model"] = model
	encoded, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return encoded
}

func rewriteSSELine(line string, alias string) (string, bool, map[string]any) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "data:") {
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "[DONE]" {
			return line, true, nil
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			return line, false, nil
		}
		if _, ok := obj["model"]; ok {
			if model, err := json.Marshal(alias); err == nil {
				obj["model"] = model
			}
		}
		usage := usageFromRawObject(obj)
		encoded, err := json.Marshal(obj)
		if err != nil {
			return line, false, usage
		}
		return "data: " + string(encoded), false, usage
	}
	return line, false, nil
}

func usageFromRawResponse(body []byte) map[string]any {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil
	}
	return usageFromRawObject(obj)
}

func usageFromRawObject(obj map[string]json.RawMessage) map[string]any {
	var usage map[string]any
	if usageRaw, ok := obj["usage"]; ok {
		usage = usageMapFromRaw(usageRaw)
	}
	// Copilot reports AI-unit consumption outside the standard usage block.
	if copilotRaw, ok := obj["copilot_usage"]; ok {
		var copilotUsage map[string]any
		if err := json.Unmarshal(copilotRaw, &copilotUsage); err == nil {
			if v, numOK := numericValue(copilotUsage["total_nano_aiu"]); numOK {
				if usage == nil {
					usage = make(map[string]any)
				}
				usage["copilot_nano_aiu"] = v
			}
		}
	}
	if usage != nil {
		return usage
	}
	if responseRaw, ok := obj["response"]; ok {
		var response map[string]json.RawMessage
		if err := json.Unmarshal(responseRaw, &response); err == nil {
			return usageFromRawObject(response)
		}
	}
	return nil
}

func usageMapFromRaw(raw json.RawMessage) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	usage := make(map[string]any)
	copyUsageNumber(usage, parsed, "prompt_tokens")
	copyUsageNumber(usage, parsed, "completion_tokens")
	copyUsageNumber(usage, parsed, "total_tokens")
	copyUsageNumber(usage, parsed, "input_tokens")
	copyUsageNumber(usage, parsed, "output_tokens")
	if _, ok := usage["total_tokens"]; !ok {
		if input, inputOK := numericValue(parsed["input_tokens"]); inputOK {
			if output, outputOK := numericValue(parsed["output_tokens"]); outputOK {
				usage["total_tokens"] = input + output
			}
		}
	}
	copyUsageCost(usage, parsed, "cost")
	copyUsageCost(usage, parsed, "cost_usd")
	copyNestedUsageNumber(usage, parsed, "prompt_tokens_details", "cached_tokens", "cache_read_tokens")
	copyNestedUsageNumber(usage, parsed, "input_tokens_details", "cached_tokens", "cache_read_tokens")
	copyNestedUsageNumber(usage, parsed, "completion_tokens_details", "reasoning_tokens", "reasoning_tokens")
	copyNestedUsageNumber(usage, parsed, "output_tokens_details", "reasoning_tokens", "reasoning_tokens")
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func copyUsageNumber(dst map[string]any, src map[string]any, key string) {
	if value, ok := numericValue(src[key]); ok {
		dst[key] = value
	}
}

func copyNestedUsageNumber(dst map[string]any, src map[string]any, parent string, key string, dstKey string) {
	obj, ok := src[parent].(map[string]any)
	if !ok {
		return
	}
	if value, ok := numericValue(obj[key]); ok {
		dst[dstKey] = value
	}
}

func copyUsageCost(dst map[string]any, src map[string]any, key string) {
	value, ok := src[key]
	if !ok {
		return
	}
	if n, ok := numericValue(value); ok {
		dst["cost_usd"] = n
		return
	}
	if s, ok := value.(string); ok {
		if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			dst["cost_usd"] = n
		}
	}
}

func numericValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
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

func inferTaskType(messages []openAIRequestMessage) string {
	if len(messages) == 0 {
		return "general"
	}
	return inferTaskTypeFromText(messages[len(messages)-1].Content.String())
}

func inferTaskTypeFromInput(input []openAIResponsesInput) string {
	if len(input) == 0 {
		return "general"
	}
	return inferTaskTypeFromText(input[len(input)-1].Content.String())
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

func convertMessages(msgs []openAIRequestMessage) []openrouter.Message {
	out := make([]openrouter.Message, len(msgs))
	for i, m := range msgs {
		out[i] = openrouter.Message{Role: m.Role, Content: m.Content.String()}
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
		out = append(out, openrouter.Message{Role: role, Content: item.Content.String()})
	}
	return out
}

// OpenAI-compatible request/response types.

type openAIChatCompletionRequest struct {
	Model       string                 `json:"model"`
	Messages    []openAIRequestMessage `json:"messages"`
	Temperature float64                `json:"temperature,omitempty"`
	MaxTokens   int                    `json:"max_tokens,omitempty"`
	Stream      bool                   `json:"stream,omitempty"`
}

type openAIRequestMessage struct {
	Role    string         `json:"role"`
	Content rawTextContent `json:"content"`
}

type rawTextContent string

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

type openAIResponsesRequest struct {
	Model  string                  `json:"model"`
	Input  openAIResponsesInputSet `json:"input"`
	Stream bool                    `json:"stream,omitempty"`
}

type openAIResponsesInput struct {
	Role    string         `json:"role"`
	Type    string         `json:"type"`
	Content rawTextContent `json:"content"`
	Output  string         `json:"output"`
	Name    string         `json:"name"`
	Args    string         `json:"arguments"`
}

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
