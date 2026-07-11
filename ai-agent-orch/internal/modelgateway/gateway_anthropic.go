package modelgateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/modelbackend"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

// Anthropic-compatible adapter: a POST /v1/messages endpoint that translates
// Anthropic Messages traffic into governed model calls. Every governance piece
// (auth, session resolution, routing, backend, audit) is reused from the
// existing gateway machinery; only the Anthropic <-> internal-chat translation
// below is new code. The non-streaming path audits as model.gateway_call and
// the streaming lane reuses model.gateway_stream.completed/.failed (the same
// types as the chat lane), so usage and cost aggregate through
// internal/governance/session_usage.go.

// --- Translation types (Data Models / Anthropic adapter types) ---

type anthropicMessageRequest struct {
	Model       string             `json:"model"` // treated as Model_Alias
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	MaxTokens   int                `json:"max_tokens"` // required by Anthropic
	Stream      bool               `json:"stream,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	ToolChoice  json.RawMessage    `json:"tool_choice,omitempty"` // string OR {type,name}
}

// anthropicTool is an Anthropic tool definition; it maps to an OpenAI
// function tool ({type:"function",function:{name,description,parameters}}).
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`    // "user" | "assistant"
	Content anthropicContent `json:"content"` // string OR []block
}

// anthropicContent accepts a bare string OR an array of content blocks and
// retains both the flattened text (Text, preserving prior behavior) and the
// structured blocks (Blocks) needed for tool translation. A plain-string
// message yields a single text block, so .String() is unchanged for text-only.
type anthropicContent struct {
	Text   string                 // flattened text, joined with "\n" (unchanged semantics)
	Blocks []anthropicContentItem // ordered blocks: text | tool_use | tool_result
}

// anthropicContentItem is one block inside a message's content array.
type anthropicContentItem struct {
	Type      string          `json:"type"`                  // "text" | "tool_use" | "tool_result"
	Text      string          `json:"text,omitempty"`        // type=text
	ID        string          `json:"id,omitempty"`          // type=tool_use (Anthropic tool-call id)
	Name      string          `json:"name,omitempty"`        // type=tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // type=tool_use (tool arguments object)
	ToolUseID string          `json:"tool_use_id,omitempty"` // type=tool_result → links to a prior tool_use id
	Content   json.RawMessage `json:"content,omitempty"`     // type=tool_result (string or text blocks)
}

func (c *anthropicContent) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		c.Text = ""
		c.Blocks = nil
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		c.Text = text
		c.Blocks = []anthropicContentItem{{Type: "text", Text: text}}
		return nil
	}
	var blocks []anthropicContentItem
	if err := json.Unmarshal(trimmed, &blocks); err == nil {
		var texts []string
		for _, b := range blocks {
			if b.Type == "text" || b.Type == "input_text" {
				texts = append(texts, b.Text)
			}
		}
		c.Text = strings.Join(texts, "\n")
		c.Blocks = blocks
		return nil
	}
	c.Text = ""
	c.Blocks = nil
	return nil
}

// MarshalJSON emits a bare JSON string when the content is a single text block
// (or empty), preserving the pre-feature text-only wire shape; otherwise it
// emits the structured block array.
func (c anthropicContent) MarshalJSON() ([]byte, error) {
	if len(c.Blocks) == 0 {
		return json.Marshal(c.Text)
	}
	if len(c.Blocks) == 1 && (c.Blocks[0].Type == "text" || c.Blocks[0].Type == "") {
		return json.Marshal(c.Blocks[0].Text)
	}
	return json.Marshal(c.Blocks)
}

func (c anthropicContent) String() string { return c.Text }

type anthropicMessageResponse struct {
	ID         string                  `json:"id"`   // "msg_..."
	Type       string                  `json:"type"` // "message"
	Role       string                  `json:"role"` // "assistant"
	Model      string                  `json:"model"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"` // "end_turn" | "max_tokens"
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicContentBlock struct {
	Type  string          `json:"type"`            // "text" | "tool_use"
	Text  string          `json:"text,omitempty"`  // type=text
	ID    string          `json:"id,omitempty"`    // type=tool_use
	Name  string          `json:"name,omitempty"`  // type=tool_use
	Input json.RawMessage `json:"input,omitempty"` // type=tool_use (parsed tool arguments)
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicStreamEvent is the stable subset of Anthropic SSE events the adapter
// emits. Each event is framed as `event: <type>\ndata: <json>\n\n`.
type anthropicStreamEvent struct {
	Type string `json:"type"`
}

// --- Request/response translation (the only new logic) ---

// toChatRequest maps an Anthropic message request to the internal chat request:
// system -> leading {role:"system"} message, max_tokens -> MaxTokens, model is
// passed through unchanged so routeModel resolves it as a Model_Alias.
func (req anthropicMessageRequest) toChatRequest() openAIChatCompletionRequest {
	msgs := make([]openAIRequestMessage, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.System) != "" {
		msgs = append(msgs, openAIRequestMessage{Role: "system", Content: rawTextContent(req.System)})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, openAIRequestMessage{Role: m.Role, Content: rawTextContent(m.Content.String())})
	}
	out := openAIChatCompletionRequest{
		Model:     req.Model,
		Messages:  msgs,
		MaxTokens: req.MaxTokens,
		Stream:    req.Stream,
	}
	if req.Temperature != nil {
		out.Temperature = *req.Temperature
	}
	return out
}

// toChatBody renders the translated request as an OpenAI chat-completions body
// for raw backends, which forward the body unchanged. All request-side tool
// translation lands here because this is the single body the backend forwards:
// tool definitions/tool_choice translate once at the top level, assistant
// tool_use blocks become tool_calls, and user tool_result blocks become
// separate {role:"tool"} messages.
func (req anthropicMessageRequest) toChatBody(alias string) []byte {
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		messages = append(messages, anthropicMessageToChatMessages(m)...)
	}
	body := map[string]any{
		"model":    alias,
		"messages": messages,
		"stream":   req.Stream,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	// Tool definitions + tool_choice translate only when tools are present, so
	// tool-free requests carry neither key (text-only wire shape unchanged).
	if tools := anthropicToolsToOpenAI(req.Tools); len(tools) > 0 {
		body["tools"] = tools
		if choice, ok := anthropicToolChoiceToOpenAI(req.ToolChoice); ok {
			body["tool_choice"] = choice
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil
	}
	return encoded
}

// anthropicMessageToChatMessages translates one Anthropic message into one or
// more OpenAI chat messages: text + assistant tool_use blocks collapse into a
// single message (content + tool_calls), and each user tool_result block
// becomes a trailing {role:"tool"} message.
func anthropicMessageToChatMessages(m anthropicMessage) []map[string]any {
	content := m.Content.String() // joined text of text blocks (tool blocks excluded)
	var toolCalls []map[string]any
	var toolResults []map[string]any
	for _, b := range m.Content.Blocks {
		switch b.Type {
		case "tool_use":
			args := strings.TrimSpace(string(b.Input))
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   b.ID,
				"type": "function",
				"function": map[string]any{
					"name":      b.Name,
					"arguments": args,
				},
			})
		case "tool_result":
			toolResults = append(toolResults, map[string]any{
				"role":         "tool",
				"tool_call_id": b.ToolUseID,
				"content":      chatContentText(b.Content),
			})
		}
	}

	out := make([]map[string]any, 0, 1+len(toolResults))
	main := map[string]any{"role": m.Role}
	includeMain := false
	if content != "" {
		main["content"] = content
		includeMain = true
	}
	if len(toolCalls) > 0 {
		main["tool_calls"] = toolCalls
		if _, ok := main["content"]; !ok {
			main["content"] = ""
		}
		includeMain = true
	}
	if includeMain {
		out = append(out, main)
	}
	out = append(out, toolResults...)
	if len(out) == 0 {
		// Preserve the message even when it carries only empty text, matching
		// the prior {role,content:""} behavior.
		out = append(out, map[string]any{"role": m.Role, "content": content})
	}
	return out
}

// anthropicToolsToOpenAI maps Anthropic tool definitions to OpenAI function
// tools, preserving name/description and forwarding input_schema verbatim as
// the function parameters.
func anthropicToolsToOpenAI(tools []anthropicTool) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn := map[string]any{"name": t.Name}
		if t.Description != "" {
			fn["description"] = t.Description
		}
		if len(bytes.TrimSpace(t.InputSchema)) > 0 {
			fn["parameters"] = json.RawMessage(t.InputSchema)
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

// anthropicToolChoiceToOpenAI maps an Anthropic tool_choice to its OpenAI
// equivalent. It returns ok=false when tool_choice is absent (the caller omits
// the key), matching the "auto/omitted → auto/omitted" rule.
func anthropicToolChoiceToOpenAI(raw json.RawMessage) (any, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, false
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, false
		}
		return s, true // "auto" / "none" / "required" pass through
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, false
	}
	switch obj.Type {
	case "auto":
		return "auto", true
	case "any":
		return "required", true
	case "none":
		return "none", true
	case "tool":
		return map[string]any{"type": "function", "function": map[string]any{"name": obj.Name}}, true
	default:
		return nil, false
	}
}

// anthropicStopReason maps an OpenAI finish_reason to an Anthropic stop_reason.
func anthropicStopReason(finishReason string) string {
	switch strings.TrimSpace(strings.ToLower(finishReason)) {
	case "length", "max_tokens":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

// --- Non-streaming handler ---

func (g *Gateway) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
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

	var req anthropicMessageRequest
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

	sessionID, session, ok := g.resolveSession(w, r, req.Model, body, "anthropic.messages")
	if !ok {
		return
	}
	finishStatus := "failed"
	defer func() {
		if finishStatus != "" {
			g.finishGatewayAutoSession(context.Background(), sessionID, session, finishStatus)
		}
	}()

	decision, err := g.routeModel(r.Context(), req.Model, session, inferTaskType(req.toChatRequest().Messages))
	if err != nil {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}

	if req.Stream {
		finishStatus = ""
		g.handleAnthropicStream(w, r, req, decision, session, sessionID, body)
		return
	}

	content, finishReason, toolCalls, usage, respBody, err := g.anthropicBackendCall(r.Context(), decision, req, session)
	if err != nil {
		g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_call", body, nil, nil, err.Error())
		httpx.WriteJSON(w, providerErrorStatus(err), map[string]any{"error": fmt.Sprintf("model provider failed: %v", err)})
		return
	}
	g.auditModelCall(r.Context(), sessionID, session, decision, "model.gateway_call", body, respBody, usage, "")
	finishStatus = "completed"

	// A text block is emitted whenever there is text, or when there are no tool
	// calls at all (so a tool-free response is always one text block, matching
	// pre-feature behavior even for empty content).
	blocks := make([]anthropicContentBlock, 0, 1+len(toolCalls))
	if content != "" || len(toolCalls) == 0 {
		blocks = append(blocks, anthropicContentBlock{Type: "text", Text: content})
	}
	for _, tc := range toolCalls {
		args := strings.TrimSpace(tc.Arguments)
		if args == "" {
			args = "{}"
		}
		blocks = append(blocks, anthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Name,
			Input: json.RawMessage(args),
		})
	}
	stopReason := anthropicStopReason(finishReason)
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}

	resp := anthropicMessageResponse{
		ID:         g.newID("msg"),
		Type:       "message",
		Role:       "assistant",
		Model:      decision.SelectedAlias, // alias, never the provider model
		Content:    blocks,
		StopReason: stopReason,
		Usage: anthropicUsage{
			InputTokens:  anthropicUsageInt(usage, "prompt_tokens", "input_tokens"),
			OutputTokens: anthropicUsageInt(usage, "completion_tokens", "output_tokens"),
		},
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// anthropicBackendCall invokes the routed backend (raw or typed, mirroring
// handleChatCompletions) and returns the assistant text, finish reason, any
// tool calls, usage map, and the upstream response bytes for auditing.
func (g *Gateway) anthropicBackendCall(ctx context.Context, decision router.Decision, req anthropicMessageRequest, session SessionInfo) (string, string, []chatToolCall, map[string]any, []byte, error) {
	if rawBackend, ok := g.backend.(modelbackend.RawChatBackend); ok {
		responsesBackend, responsesOK := g.backend.(modelbackend.RawResponsesBackend)
		chatBody := req.toChatBody(decision.SelectedAlias)
		chatReq := req.toChatRequest()

		// Pre-check: Copilot Responses-only models (GPT-5.x class) go straight
		// to the bridge, mirroring handleChatCompletions.
		if responsesOK && copilotModelUsesResponsesAPI(decision.Provider, decision.SelectedModelID) {
			respBody, usage, err := responsesRawAsChatCompletion(ctx, responsesBackend, decision, chatReq, session.ActorSubject, chatBody)
			if err != nil {
				return "", "", nil, nil, nil, err
			}
			content, finish, toolCalls := chatCompletionContentFinishTools(respBody)
			return content, finish, toolCalls, usage, respBody, nil
		}

		respBody, err := rawBackend.ChatCompletionRaw(ctx, modelbackend.RawRequest{
			Provider:     decision.Provider,
			ModelAlias:   decision.SelectedAlias,
			Model:        decision.SelectedModelID,
			Body:         chatBody,
			ActorSubject: session.ActorSubject,
		})
		if err != nil {
			// Fallback: a chat-completions Responses-only error retries once via
			// the bridge, mirroring handleChatCompletions.
			if responsesOK && responsesOnlyChatError(err) {
				retryBody, usage, retryErr := responsesRawAsChatCompletion(ctx, responsesBackend, decision, chatReq, session.ActorSubject, chatBody)
				if retryErr == nil {
					content, finish, toolCalls := chatCompletionContentFinishTools(retryBody)
					return content, finish, toolCalls, usage, retryBody, nil
				}
				err = retryErr
			}
			return "", "", nil, nil, nil, err
		}
		respBody = rewriteTopLevelModel(respBody, decision.SelectedAlias)
		content, finish, toolCalls := chatCompletionContentFinishTools(respBody)
		return content, finish, toolCalls, usageFromRawResponse(respBody), respBody, nil
	}

	upstream := openrouter.ChatCompletionRequest{
		Provider:    decision.Provider,
		ModelAlias:  decision.SelectedAlias,
		Model:       decision.SelectedModelID,
		Messages:    convertMessages(req.toChatRequest().Messages),
		Temperature: req.toChatRequest().Temperature,
		MaxTokens:   req.MaxTokens,
	}
	resp, err := g.backend.ChatCompletion(ctx, upstream)
	if err != nil {
		return "", "", nil, nil, nil, err
	}
	respBody, _ := json.Marshal(resp)
	return resp.FirstContent(), "", nil, openrouterUsageMap(&resp.Usage), respBody, nil
}

// chatToolCall is a tool call extracted from an OpenAI chat completion response.
type chatToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// chatCompletionContentFinishTools extracts choices[0].message.content,
// finish_reason, and any tool_calls from a raw OpenAI chat completion response.
func chatCompletionContentFinishTools(body []byte) (string, string, []chatToolCall) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Choices) == 0 {
		return "", "", nil
	}
	choice := parsed.Choices[0]
	var toolCalls []chatToolCall
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, chatToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return choice.Message.Content, choice.FinishReason, toolCalls
}

// anthropicUsageInt returns the first present numeric usage key, letting the
// chat-completions path (prompt_tokens/completion_tokens) and the bridged
// Responses path (input_tokens/output_tokens) both map cleanly.
func anthropicUsageInt(usage map[string]any, keys ...string) int {
	if usage == nil {
		return 0
	}
	for _, key := range keys {
		if v, ok := numericValue(usage[key]); ok {
			return int(v)
		}
	}
	return 0
}

// --- Streaming handler (Streaming Translation Mapping) ---

func (g *Gateway) handleAnthropicStream(w http.ResponseWriter, r *http.Request, req anthropicMessageRequest, decision router.Decision, session SessionInfo, sessionID string, reqBody []byte) {
	finishStatus := "failed"
	defer func() {
		g.finishGatewayAutoSession(context.Background(), sessionID, session, finishStatus)
	}()
	reqHash := sha256Hex(reqBody)

	chatReq := req.toChatRequest()
	streamBody := ensureChatStreamOptions(req.toChatBody(decision.SelectedAlias))
	streamReader, err := startChatStream(r.Context(), g.backend, decision, chatReq, session.ActorSubject, streamBody)
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

	responseHash := sha256.New()

	// 1. message_start
	_ = writeAnthropicEvent(w, flusher, responseHash, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":          g.newID("msg"),
			"type":        "message",
			"role":        "assistant",
			"model":       decision.SelectedAlias,
			"content":     []any{},
			"stop_reason": nil,
			"usage":       map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
	// 2. content_block_start (index 0, text)
	_ = writeAnthropicEvent(w, flusher, responseHash, "content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})

	scanner := bufio.NewScanner(streamReader)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)
	stopReason := "end_turn"
	outputTokens := 0
	done := false
	toolBlocks := map[int]*anthropicToolBlockState{}
	var toolOrder []int // started tool indices, in first-seen order
	for scanner.Scan() {
		select {
		case <-r.Context().Done():
			g.auditModelCallHashes(context.Background(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", r.Context().Err().Error())
			return
		default:
		}
		text, finishReason, usage, toolDeltas, streamDone := anthropicDeltaFromChatSSE(scanner.Text())
		if finishReason != "" {
			if strings.EqualFold(strings.TrimSpace(finishReason), "tool_calls") {
				stopReason = "tool_use"
			} else {
				stopReason = anthropicStopReason(finishReason)
			}
		}
		if usage != nil {
			if v, ok := numericValue(usage["completion_tokens"]); ok {
				outputTokens = int(v)
			}
		}
		if text != "" {
			// 3..n content_block_delta (one per chat delta.content chunk)
			_ = writeAnthropicEvent(w, flusher, responseHash, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": text},
			})
		}
		// Tool-call deltas: Anthropic block index = OpenAI tool index + 1 (text
		// keeps index 0). Each new index opens a tool_use block; arg fragments
		// stream as input_json_delta.
		for _, td := range toolDeltas {
			state := toolBlocks[td.Index]
			if state == nil {
				state = &anthropicToolBlockState{anthropicIndex: td.Index + 1}
				toolBlocks[td.Index] = state
				toolOrder = append(toolOrder, td.Index)
			}
			if !state.started {
				_ = writeAnthropicEvent(w, flusher, responseHash, "content_block_start", map[string]any{
					"type":          "content_block_start",
					"index":         state.anthropicIndex,
					"content_block": map[string]any{"type": "tool_use", "id": td.ID, "name": td.Name, "input": map[string]any{}},
				})
				state.started = true
			}
			if td.ArgsFrag != "" {
				_ = writeAnthropicEvent(w, flusher, responseHash, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": state.anthropicIndex,
					"delta": map[string]any{"type": "input_json_delta", "partial_json": td.ArgsFrag},
				})
			}
		}
		if streamDone {
			done = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", err.Error())
		return
	}
	// A backend stream that ends before [DONE] is a mid-stream failure: record a
	// stream-failure audit event rather than a spurious completion (Req 5.4),
	// mirroring handleStream's done-tracking.
	if !done {
		g.auditModelCallHashes(r.Context(), sessionID, session, decision, "model.gateway_stream.failed", reqHash, "", "stream ended before done")
		return
	}

	// n+1 content_block_stop for the text block (index 0)
	_ = writeAnthropicEvent(w, flusher, responseHash, "content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": 0,
	})
	// one content_block_stop per started tool block (first-seen order)
	for _, idx := range toolOrder {
		if state := toolBlocks[idx]; state != nil && state.started {
			_ = writeAnthropicEvent(w, flusher, responseHash, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": state.anthropicIndex,
			})
		}
	}
	// n+2 message_delta (stop_reason + output_tokens)
	_ = writeAnthropicEvent(w, flusher, responseHash, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason},
		"usage": map[string]any{"output_tokens": outputTokens},
	})
	// n+3 message_stop
	_ = writeAnthropicEvent(w, flusher, responseHash, "message_stop", map[string]any{
		"type": "message_stop",
	})

	respHash := "sha256:" + hex.EncodeToString(responseHash.Sum(nil))
	streamUsage := map[string]any{"completion_tokens": outputTokens}
	g.auditModelCallHashesWithUsage(r.Context(), sessionID, session, decision, "model.gateway_stream.completed", reqHash, respHash, streamUsage, "")
	finishStatus = "completed"
}

// chatToolCallDelta is a single streamed OpenAI tool-call fragment.
type chatToolCallDelta struct {
	Index    int    // OpenAI tool index
	ID       string // present on the first fragment
	Name     string // present on the first fragment
	ArgsFrag string // function.arguments fragment (may be empty)
}

// anthropicToolBlockState tracks an open Anthropic tool_use content block.
type anthropicToolBlockState struct {
	anthropicIndex int  // openAI tool index + 1
	started        bool // content_block_start{tool_use} emitted
}

// anthropicDeltaFromChatSSE parses one OpenAI chat-completion SSE line and
// returns the delta text, finish reason, usage, any tool-call deltas, and
// whether the stream is done.
func anthropicDeltaFromChatSSE(line string) (string, string, map[string]any, []chatToolCallDelta, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", "", nil, nil, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" {
		return "", "", nil, nil, false
	}
	if payload == "[DONE]" {
		return "", "", nil, nil, true
	}
	var parsed struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return "", "", nil, nil, false
	}
	var obj map[string]json.RawMessage
	_ = json.Unmarshal([]byte(payload), &obj)
	usage := usageFromRawObject(obj)
	if len(parsed.Choices) == 0 {
		return "", "", usage, nil, false
	}
	choice := parsed.Choices[0]
	var toolDeltas []chatToolCallDelta
	for _, tc := range choice.Delta.ToolCalls {
		toolDeltas = append(toolDeltas, chatToolCallDelta{
			Index:    tc.Index,
			ID:       tc.ID,
			Name:     tc.Function.Name,
			ArgsFrag: tc.Function.Arguments,
		})
	}
	return choice.Delta.Content, choice.FinishReason, usage, toolDeltas, false
}

func writeAnthropicEvent(w io.Writer, flusher http.Flusher, h hash.Hash, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame := "event: " + eventType + "\ndata: " + string(data) + "\n\n"
	_, _ = h.Write([]byte(frame))
	if _, err := io.WriteString(w, frame); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
