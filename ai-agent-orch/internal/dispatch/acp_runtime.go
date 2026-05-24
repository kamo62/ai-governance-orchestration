package dispatch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
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
	go handle.sendInitialize()

	return handle, nil
}

type acpHandle struct {
	config SessionConfig
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
	cancel func()
	events chan RuntimeEvent
	done   chan struct{}
	err    error
	mu     sync.Mutex
	nextID int
}

func (h *acpHandle) sendInitialize() {
	// JSON-RPC 2.0 initialize request.
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      h.allocID(),
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    "ai-agent-orch",
				"version": "0.2.0-alpha",
			},
		},
	}
	if err := h.sendJSON(initReq); err != nil {
		h.emitError(fmt.Sprintf("initialize send failed: %v", err))
		return
	}

	// Send initialized notification.
	notify := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	if err := h.sendJSON(notify); err != nil {
		h.emitError(fmt.Sprintf("initialized send failed: %v", err))
		return
	}

	// Send the prompt as a tool call or message.
	promptReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      h.allocID(),
		"method":  "tools/call",
		"params": map[string]any{
			"name": "execute_task",
			"arguments": map[string]string{
				"prompt":       defaultUserPrompt(h.config.UserPrompt),
				"systemPrompt": h.config.SystemPrompt,
			},
		},
	}
	if err := h.sendJSON(promptReq); err != nil {
		h.emitError(fmt.Sprintf("prompt send failed: %v", err))
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
	defer close(h.done)
	defer close(h.events)
	defer h.stdin.Close()
	defer h.cancel()

	go func() {
		// Drain stderr to prevent blocking.
		io.Copy(io.Discard, stderr)
	}()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var msg jsonRPCMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			h.events <- RuntimeEvent{
				Type:    "stream",
				Payload: line,
			}
			continue
		}

		if msg.Error != nil {
			h.events <- RuntimeEvent{
				Type:    "error",
				Payload: fmt.Sprintf("ACP error: %v", msg.Error),
			}
			continue
		}

		if msg.Method == "notifications/message" || msg.Method == "notifications/progress" {
			var payload string
			if p, ok := msg.Params["message"]; ok {
				payload = fmt.Sprintf("%v", p)
			} else if p, ok := msg.Params["content"]; ok {
				payload = fmt.Sprintf("%v", p)
			} else {
				payload = fmt.Sprintf("%v", msg.Params)
			}
			h.events <- RuntimeEvent{
				Type:    "stream",
				Payload: payload,
			}
			continue
		}

		// Response to our initialize or tool call.
		if msg.Result != nil {
			resultJSON, _ := json.Marshal(msg.Result)
			h.events <- RuntimeEvent{
				Type:    "stream",
				Payload: string(resultJSON),
			}
		}
	}

	if err := scanner.Err(); err != nil {
		h.err = err
		h.emitError(fmt.Sprintf("ACP stream error: %v", err))
	}

	h.events <- RuntimeEvent{
		Type:    "done",
		Payload: "ACP session complete",
	}
}

func (h *acpHandle) emitError(msg string) {
	select {
	case h.events <- RuntimeEvent{Type: "error", Payload: msg}:
	default:
	}
}

func (h *acpHandle) Wait() error {
	<-h.done
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
