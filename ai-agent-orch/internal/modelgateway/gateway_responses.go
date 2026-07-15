package modelgateway

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/modelbackend"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

func (g *Gateway) handleResponses(w http.ResponseWriter, r *http.Request) {
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

	var req openAIResponsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Model == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "model is required"})
		return
	}
	if len(req.Input) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "input is required"})
		return
	}
	sessionID, session, ok := g.resolveSession(w, r, req.Model, body, "responses")
	if !ok {
		return
	}
	finishStatus := "failed"
	defer func() {
		if finishStatus != "" {
			g.finishGatewayAutoSession(context.Background(), sessionID, session, finishStatus)
		}
	}()

	decision, err := g.routeModel(r.Context(), req.Model, session, inferTaskTypeFromInput(req.Input))
	if err != nil {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}
	body, decision, err = applyGovernedReasoning(body, decision, session, "responses")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if rawBackend, ok := g.backend.(modelbackend.RawResponsesBackend); ok {
		if req.Stream {
			finishStatus = ""
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
			httpx.WriteJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
			return
		}
		respBody = rewriteTopLevelModel(respBody, decision.SelectedAlias)
		usage := usageFromRawResponse(respBody)
		g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_responses", body, respBody, usage, "")
		finishStatus = "completed"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBody)
		return
	}

	if req.Stream {
		httpx.WriteJSON(w, http.StatusNotImplemented, map[string]any{"error": "responses streaming is not supported by the selected backend"})
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
		httpx.WriteJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}

	respBody, _ := json.Marshal(resp)
	g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_responses", body, respBody, openrouterUsageMap(&resp.Usage), "")
	finishStatus = "completed"

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

	httpx.WriteJSON(w, http.StatusOK, openAIResp)
}

func (g *Gateway) handleResponsesStream(w http.ResponseWriter, r *http.Request, backend modelbackend.RawResponsesBackend, decision router.Decision, session SessionInfo, sessionID string, reqBody []byte) {
	finishStatus := "failed"
	defer func() {
		g.finishGatewayAutoSession(context.Background(), sessionID, session, finishStatus)
	}()
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
		httpx.WriteJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("responses stream start failed: %v", err)})
		return
	}
	defer streamReader.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.failed", reqHash, "", "streaming not supported")
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
	incomplete := false
	var streamUsage map[string]any
	toolCalls := newToolCallAuditTracker()
	for scanner.Scan() {
		select {
		case <-r.Context().Done():
			g.auditModelCallHashesWithUsageAndTools(context.Background(), sessionID, session, decision, "model.gateway_responses_stream.failed", reqHash, "", nil, toolCalls.Summary(), r.Context().Err().Error())
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
		if strings.Contains(line, "response.incomplete") {
			incomplete = true
		}
		if lineDone || strings.Contains(line, "response.completed") || incomplete {
			done = true
		}
		if usage != nil {
			streamUsage = usage
		}
		toolCalls.ObserveResponsesSSELine(line)
		frame := line + "\n"
		_, _ = responseHash.Write([]byte(frame))
		fmt.Fprint(w, frame)
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil {
		g.auditModelCallHashesWithUsageAndTools(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.failed", reqHash, "", nil, toolCalls.Summary(), err.Error())
		return
	}
	if !done {
		g.auditModelCallHashesWithUsageAndTools(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.failed", reqHash, "", nil, toolCalls.Summary(), "stream ended before completion")
		return
	}
	respHash := "sha256:" + hex.EncodeToString(responseHash.Sum(nil))
	if incomplete {
		// Truncated runs (for example max_output_tokens) must not look like
		// successful completions in the audit ledger.
		g.auditModelCallHashesWithUsageAndTools(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.incomplete", reqHash, respHash, streamUsage, toolCalls.Summary(), "provider reported response.incomplete")
		return
	}
	g.auditModelCallHashesWithUsageAndTools(r.Context(), sessionID, session, decision, "model.gateway_responses_stream.completed", reqHash, respHash, streamUsage, toolCalls.Summary(), "")
	finishStatus = "completed"
}
