package dispatch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"ai-agent-orch/internal/appversion"
)

// ACPRuntime implements Runtime using OpenCode ACP JSON-RPC over stdio.
// This is the preferred Phase 1 runtime path.
type ACPRuntime struct {
	binaryPath string
	timeout    time.Duration
}

func NewACPRuntime(binaryPath string) *ACPRuntime {
	if binaryPath == "" {
		binaryPath = "opencode"
	}
	return &ACPRuntime{
		binaryPath: binaryPath,
		timeout:    15 * time.Minute,
	}
}

func (r *ACPRuntime) StartSession(ctx context.Context, cfg SessionConfig) (SessionHandle, error) {
	if cfg.ModelID == "" {
		return nil, fmt.Errorf("model_id is required")
	}
	runCtx := ctx
	cancel := func() {}
	if r.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.timeout)
	}

	// Check if opencode binary exists.
	path, err := exec.LookPath(r.binaryPath)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("opencode not found in PATH: %w", err)
	}

	handle := &acpHandle{
		ctx:    runCtx,
		config: cfg,
		events: make(chan RuntimeEvent, 64),
		done:   make(chan struct{}),
	}

	cmd := exec.CommandContext(runCtx, path, "acp")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	handle.stdin = stdin
	handle.stdout = stdout
	handle.cmd = cmd
	handle.cancel = cancel

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start opencode acp: %w", err)
	}

	go handle.readLoop(stdout, stderr)
	go handle.runSession()

	return handle, nil
}

type acpHandle struct {
	ctx       context.Context
	config    SessionConfig
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	cmd       *exec.Cmd
	cancel    func()
	events    chan RuntimeEvent
	done      chan struct{}
	err       error
	mu        sync.Mutex
	eventMu   sync.Mutex
	closed    bool
	nextID    int
	pending   map[int]chan *jsonRPCMessage
	pendingMu sync.Mutex
	sessionID string
	// Accumulates text content from agent_message_chunk for patch extraction.
	accumulatedText strings.Builder
	accumulatedMu   sync.Mutex
	// requestTimeout is the timeout for individual JSON-RPC requests.
	// Defaults to 5 minutes; override in tests.
	requestTimeout time.Duration
}

func (h *acpHandle) runSession() {
	defer close(h.done)
	defer h.closeEvents()
	defer h.stdin.Close()
	defer h.cancel()

	// 1. Initialize
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      h.allocID(),
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": 1,
			"capabilities": map[string]any{
				"fs": map[string]any{
					"readTextFile":  true,
					"writeTextFile": false,
				},
			},
			"clientInfo": map[string]string{
				"name":    "ai-agent-orch",
				"version": appversion.Version,
			},
		},
	}
	initResp, err := h.sendRequest(initReq)
	if err != nil {
		h.emitError(fmt.Sprintf("initialize failed: %v", err))
		return
	}
	if initResp.Error != nil {
		h.emitError(fmt.Sprintf("initialize error: %v", initResp.Error))
		return
	}

	workspace, err := h.workspacePath()
	if err != nil {
		h.emitError(err.Error())
		return
	}

	// 2. Create session
	newSessionReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      h.allocID(),
		"method":  "session/new",
		"params": map[string]any{
			"cwd":        workspace,
			"mcpServers": acpMCPServers(h.config.MCPEndpoints, os.Getenv("AI_ORCH_SERVICE_TOKEN"), h.config.SessionID),
		},
	}
	newSessionResp, err := h.sendRequest(newSessionReq)
	if err != nil {
		h.emitError(fmt.Sprintf("session/new failed: %v", err))
		return
	}
	if newSessionResp.Error != nil {
		h.emitError(fmt.Sprintf("session/new error: %v", newSessionResp.Error))
		return
	}
	if sid, ok := newSessionResp.Result["sessionId"].(string); ok {
		h.sessionID = sid
	} else {
		h.emitError("session/new response missing sessionId")
		return
	}

	// 3. Send prompt
	prompt := defaultUserPrompt(h.config.UserPrompt)
	promptReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      h.allocID(),
		"method":  "session/prompt",
		"params": map[string]any{
			"sessionId": h.sessionID,
			"prompt": []map[string]any{
				{
					"type": "text",
					"text": prompt,
				},
			},
		},
	}
	promptResp, err := h.sendRequest(promptReq)
	if err != nil {
		h.emitError(fmt.Sprintf("session/prompt failed: %v", err))
		return
	}
	if promptResp.Error != nil {
		h.emitError(fmt.Sprintf("session/prompt error: %v", promptResp.Error))
		return
	}

	// 4. Extract patches from accumulated text and final response
	h.accumulatedMu.Lock()
	accText := h.accumulatedText.String()
	h.accumulatedMu.Unlock()
	if patchPayload := extractPatchFromText(accText); patchPayload != "" {
		h.emitEvent(RuntimeEvent{Type: "patch", Payload: patchPayload})
	}
	if patchPayload := extractPatchFromResult(promptResp.Result); patchPayload != "" {
		h.emitEvent(RuntimeEvent{Type: "patch", Payload: patchPayload})
	}

	// 5. Close session
	closeReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      h.allocID(),
		"method":  "session/close",
		"params": map[string]any{
			"sessionId": h.sessionID,
		},
	}
	_, _ = h.sendRequest(closeReq)

	h.emitEvent(RuntimeEvent{Type: "done", Payload: "ACP session complete"})
}

func (h *acpHandle) sendRequest(req map[string]any) (*jsonRPCMessage, error) {
	idVal, ok := req["id"].(int)
	if !ok {
		return nil, fmt.Errorf("request missing id")
	}

	respCh := make(chan *jsonRPCMessage, 1)
	h.pendingMu.Lock()
	if h.pending == nil {
		h.pending = make(map[int]chan *jsonRPCMessage)
	}
	h.pending[idVal] = respCh
	h.pendingMu.Unlock()

	if err := h.sendJSON(req); err != nil {
		h.pendingMu.Lock()
		delete(h.pending, idVal)
		h.pendingMu.Unlock()
		return nil, err
	}

	timeout := h.requestTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case resp := <-respCh:
		return resp, nil
	case <-ctx.Done():
		h.pendingMu.Lock()
		delete(h.pending, idVal)
		h.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-time.After(timeout):
		h.pendingMu.Lock()
		delete(h.pending, idVal)
		h.pendingMu.Unlock()
		return nil, fmt.Errorf("request %d timed out", idVal)
	}
}

func (h *acpHandle) sendJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(h.stdin, "%s\n", string(data))
	return err
}

func (h *acpHandle) allocID() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	return h.nextID
}

func (h *acpHandle) readLoop(stdout, stderr io.ReadCloser) {
	go func() {
		// Drain stderr to prevent blocking.
		io.Copy(io.Discard, stderr)
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var msg jsonRPCMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			// Not valid JSON-RPC; emit as raw stream for debugging.
			h.emitEvent(RuntimeEvent{Type: "stream", Payload: line})
			continue
		}

		// Distinguish between agent requests/responses/notifications.
		if msg.ID == nil && msg.Method != "" {
			// Notification from agent.
			h.handleNotification(&msg)
			continue
		}

		if msg.ID != nil && msg.Method != "" {
			// Request from agent to client (e.g., session/request_permission).
			h.handleAgentRequest(&msg)
			continue
		}

		if msg.ID != nil {
			// Response to one of our requests.
			h.pendingMu.Lock()
			if ch, ok := h.pending[*msg.ID]; ok {
				ch <- &msg
				delete(h.pending, *msg.ID)
			}
			h.pendingMu.Unlock()
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		h.mu.Lock()
		h.err = err
		h.mu.Unlock()
		h.emitError(fmt.Sprintf("ACP stream error: %v", err))
		h.failPending(fmt.Errorf("ACP stream error: %w", err))
		return
	}
	h.failPending(io.EOF)
}

func (h *acpHandle) handleNotification(msg *jsonRPCMessage) {
	switch msg.Method {
	case "session/update":
		h.handleSessionUpdate(msg)
	default:
		// Unknown notification; ignore.
	}
}

func (h *acpHandle) handleAgentRequest(msg *jsonRPCMessage) {
	switch msg.Method {
	case "session/request_permission":
		// Fail closed until permission requests are wired through the governed tool broker.
		reqID := ""
		if p, ok := msg.Params["requestId"].(string); ok {
			reqID = p
		}
		toolName := "unknown"
		if toolCall, ok := msg.Params["toolCall"].(map[string]any); ok {
			if name, ok := toolCall["name"].(string); ok && name != "" {
				toolName = name
			}
		}
		h.emitEvent(RuntimeEvent{Type: "tool_call", Payload: fmt.Sprintf("ACP permission requested for %s", toolName)})
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      msg.ID,
			"result": map[string]any{
				"requestId": reqID,
				"outcome":   "denied",
				"message":   "permission requests must pass through the Governance Shell tool broker",
			},
		}
		_ = h.sendJSON(resp)
	default:
		h.emitEvent(RuntimeEvent{Type: "error", Payload: fmt.Sprintf("unsupported ACP request: %s", msg.Method)})
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      msg.ID,
			"error": map[string]any{
				"code":    -32601,
				"message": "unsupported request",
			},
		}
		_ = h.sendJSON(resp)
	}
}

func (h *acpHandle) handleSessionUpdate(msg *jsonRPCMessage) {
	update, ok := msg.Params["update"].(map[string]any)
	if !ok {
		return
	}

	sessionUpdateType, _ := update["sessionUpdate"].(string)
	switch sessionUpdateType {
	case "agent_message_chunk":
		if content, ok := update["content"].(map[string]any); ok {
			if text, ok := content["text"].(string); ok {
				h.emitEvent(RuntimeEvent{Type: "stream", Payload: text})
				h.accumulatedMu.Lock()
				h.accumulatedText.WriteString(text)
				h.accumulatedMu.Unlock()
			}
		}
	case "agent_thought_chunk":
		if content, ok := update["content"].(map[string]any); ok {
			if text, ok := content["text"].(string); ok {
				h.emitEvent(RuntimeEvent{Type: "stream", Payload: fmt.Sprintf("[think] %s", text)})
			}
		}
	case "tool_call":
		if toolCall, ok := update["toolCall"].(map[string]any); ok {
			if name, ok := toolCall["name"].(string); ok {
				h.emitEvent(RuntimeEvent{Type: "tool_call", Payload: fmt.Sprintf("[tool] %s", name)})
			}
		}
	case "tool_call_update":
		if toolCall, ok := update["toolCall"].(map[string]any); ok {
			if name, ok := toolCall["name"].(string); ok {
				h.emitEvent(RuntimeEvent{Type: "stream", Payload: fmt.Sprintf("[tool_update] %s", name)})
			}
		}
	case "available_commands_update", "current_mode_update", "config_option_update", "session_info_update", "usage_update":
		// Ignore non-content updates.
	default:
		// Unhandled update type; emit raw for debugging.
		if data, err := json.Marshal(update); err == nil {
			h.emitEvent(RuntimeEvent{Type: "stream", Payload: string(data)})
		}
	}
}

func (h *acpHandle) emitError(msg string) {
	h.emitEvent(RuntimeEvent{Type: "error", Payload: msg})
}

func (h *acpHandle) emitEvent(event RuntimeEvent) {
	h.eventMu.Lock()
	defer h.eventMu.Unlock()
	if h.closed {
		return
	}
	if isCriticalRuntimeEvent(event.Type) {
		var done <-chan struct{}
		if h.ctx != nil {
			done = h.ctx.Done()
		}
		select {
		case h.events <- event:
		case <-done:
		}
		return
	}
	select {
	case h.events <- event:
	default:
	}
}

func isCriticalRuntimeEvent(eventType string) bool {
	switch eventType {
	case "patch", "done", "error":
		return true
	default:
		return false
	}
}

func (h *acpHandle) closeEvents() {
	h.eventMu.Lock()
	defer h.eventMu.Unlock()
	if h.closed {
		return
	}
	close(h.events)
	h.closed = true
}

func (h *acpHandle) Wait() error {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (h *acpHandle) Events() <-chan RuntimeEvent {
	return h.events
}

func (h *acpHandle) Stop() error {
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Signal(os.Interrupt)
		if h.cancel != nil {
			h.cancel()
		}
		return h.cmd.Wait()
	}
	return nil
}

// extractPatchFromResult attempts to extract a patch envelope JSON string from an ACP result.
func extractPatchFromResult(result map[string]any) string {
	if result == nil {
		return ""
	}
	if content, ok := result["content"].(string); ok && content != "" {
		return content
	}
	if payload, ok := result["patch"].(string); ok && payload != "" {
		return payload
	}
	if payload, ok := result["output"].(string); ok && payload != "" {
		return payload
	}
	return ""
}

// extractPatchFromText scans raw text for a JSON patch envelope.
func extractPatchFromText(text string) string {
	// Look for JSON object starting with {"patch" or {"file_path"
	// This is a heuristic for extracting inline patch JSON from agent text output.
	for i := 0; i < len(text); i++ {
		if text[i] == '{' {
			// Try to find matching closing brace
			depth := 1
			for j := i + 1; j < len(text) && depth > 0; j++ {
				if text[j] == '{' {
					depth++
				} else if text[j] == '}' {
					depth--
				}
				if depth == 0 {
					sub := text[i : j+1]
					// Heuristic: must contain patch-related keys
					if containsPatchKey(sub) {
						return sub
					}
					break
				}
			}
		}
	}
	return ""
}

func containsPatchKey(s string) bool {
	if len(s) <= 20 || !strings.Contains(s, "\"files\"") {
		return false
	}
	return strings.Contains(s, "\"patch\"") ||
		strings.Contains(s, "\"patchId\"") ||
		strings.Contains(s, "\"patch_id\"")
}

func (h *acpHandle) workspacePath() (string, error) {
	if h != nil && h.config.WorkspacePath != "" {
		return h.config.WorkspacePath, nil
	}
	return "", fmt.Errorf("ACP workspace path is required")
}

func acpMCPServers(endpoints map[string]string, serviceToken string, sessionID string) []map[string]any {
	if len(endpoints) == 0 {
		return []map[string]any{}
	}
	names := make([]string, 0, len(endpoints))
	for name := range endpoints {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]map[string]any, 0, len(names))
	for _, name := range names {
		if endpoints[name] == "" {
			continue
		}
		server := map[string]any{
			"name": name,
			"url":  endpoints[name],
		}
		headers := map[string]string{}
		if serviceToken != "" {
			headers["Authorization"] = "Bearer " + serviceToken
		}
		if sessionID != "" {
			headers["X-AI-Orch-Session-ID"] = sessionID
		}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		servers = append(servers, server)
	}
	return servers
}

func (h *acpHandle) failPending(err error) {
	if h == nil {
		return
	}
	if err == nil {
		err = io.EOF
	}
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	for id, ch := range h.pending {
		ch <- &jsonRPCMessage{Error: &jsonRPCError{Code: -32000, Message: err.Error()}}
		delete(h.pending, id)
	}
}

type jsonRPCMessage struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      *int           `json:"id,omitempty"`
	Method  string         `json:"method,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *jsonRPCError  `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
