package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestACPHandle_sendRequest_ResponseRouting(t *testing.T) {
	h := &acpHandle{
		events: make(chan RuntimeEvent, 64),
		done:   make(chan struct{}),
		stdin:  &fakeWriteCloser{},
	}

	respCh := make(chan *jsonRPCMessage, 1)
	h.pending = map[int]chan *jsonRPCMessage{1: respCh}

	// Simulate readLoop receiving a response.
	go func() {
		time.Sleep(10 * time.Millisecond)
		h.pendingMu.Lock()
		if ch, ok := h.pending[1]; ok {
			ch <- &jsonRPCMessage{
				JSONRPC: "2.0",
				ID:      intPtr(1),
				Result:  map[string]any{"sessionId": "sess_123"},
			}
			delete(h.pending, 1)
		}
		h.pendingMu.Unlock()
	}()

	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{}}
	resp, err := h.sendRequest(req)
	if err != nil {
		t.Fatalf("sendRequest unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if sid, ok := resp.Result["sessionId"].(string); !ok || sid != "sess_123" {
		t.Fatalf("expected sessionId sess_123, got %v", resp.Result["sessionId"])
	}
}

func TestACPHandle_sendRequest_Timeout(t *testing.T) {
	h := &acpHandle{
		events:         make(chan RuntimeEvent, 64),
		done:           make(chan struct{}),
		stdin:          &fakeWriteCloser{},
		requestTimeout: 50 * time.Millisecond,
	}
	// Don't register any pending channel so it times out.
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{}}
	_, err := h.sendRequest(req)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if err.Error() != "request 1 timed out" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestACPHandle_sendRequest_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &acpHandle{
		ctx:            ctx,
		events:         make(chan RuntimeEvent, 64),
		done:           make(chan struct{}),
		stdin:          &fakeWriteCloser{},
		requestTimeout: time.Minute,
	}
	cancel()

	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{}}
	_, err := h.sendRequest(req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if _, ok := h.pending[1]; ok {
		t.Fatal("pending request should be removed after cancellation")
	}
}

func TestACPHandle_failPendingUnblocksRequests(t *testing.T) {
	h := &acpHandle{
		events: make(chan RuntimeEvent, 64),
		done:   make(chan struct{}),
		stdin:  &fakeWriteCloser{},
	}
	respCh := make(chan *jsonRPCMessage, 1)
	h.pending = map[int]chan *jsonRPCMessage{1: respCh}
	h.failPending(errors.New("stream closed"))

	resp := <-respCh
	if resp.Error == nil || resp.Error.Message != "stream closed" {
		t.Fatalf("expected synthetic stream-closed error, got %#v", resp)
	}
	if len(h.pending) != 0 {
		t.Fatal("pending map should be empty")
	}
}

func TestACPHandle_WorkspacePath(t *testing.T) {
	h := &acpHandle{
		config: SessionConfig{
			WorkspacePath: "/workspace/project",
			SessionID:     "sess_acp",
		},
	}
	got, err := h.workspacePath()
	if err != nil {
		t.Fatalf("workspacePath returned error: %v", err)
	}
	if got != "/workspace/project" {
		t.Fatalf("unexpected workspace path: %s", got)
	}
}

func TestACPHandle_WorkspacePathRequiresExplicitConfig(t *testing.T) {
	h := &acpHandle{}
	_, err := h.workspacePath()
	if err == nil {
		t.Fatal("expected missing workspace path to fail closed")
	}
	if err.Error() != "ACP workspace path is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestACPHandle_CriticalEventsWaitForBufferSpace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &acpHandle{
		ctx:    ctx,
		events: make(chan RuntimeEvent, 1),
	}
	h.emitEvent(RuntimeEvent{Type: "stream", Payload: "fills buffer"})

	delivered := make(chan struct{})
	go func() {
		h.emitEvent(RuntimeEvent{Type: "patch", Payload: "critical patch"})
		close(delivered)
	}()

	select {
	case <-delivered:
		t.Fatal("critical event should wait while buffer is full")
	case <-time.After(25 * time.Millisecond):
	}

	<-h.events

	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("critical event was not delivered after buffer space became available")
	}

	evt := <-h.events
	if evt.Type != "patch" || evt.Payload != "critical patch" {
		t.Fatalf("unexpected critical event: %+v", evt)
	}
}

func TestACPHandle_handleSessionUpdate_agentMessageChunk(t *testing.T) {
	h := &acpHandle{
		events: make(chan RuntimeEvent, 64),
		done:   make(chan struct{}),
	}

	msg := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params: map[string]any{
			"sessionId": "sess_123",
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content": map[string]any{
					"type": "text",
					"text": "Hello world",
				},
			},
		},
	}
	h.handleSessionUpdate(msg)

	select {
	case evt := <-h.events:
		if evt.Type != "stream" || evt.Payload != "Hello world" {
			t.Fatalf("unexpected event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected stream event")
	}

	if h.accumulatedText.String() != "Hello world" {
		t.Fatalf("expected accumulated text 'Hello world', got %q", h.accumulatedText.String())
	}
}

func TestACPHandle_handleSessionUpdate_toolCall(t *testing.T) {
	h := &acpHandle{
		events: make(chan RuntimeEvent, 64),
		done:   make(chan struct{}),
	}

	msg := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params: map[string]any{
			"sessionId": "sess_123",
			"update": map[string]any{
				"sessionUpdate": "tool_call",
				"toolCall": map[string]any{
					"name": "bash",
				},
			},
		},
	}
	h.handleSessionUpdate(msg)

	select {
	case evt := <-h.events:
		if evt.Type != "tool_call" || evt.Payload != "[tool] bash" {
			t.Fatalf("unexpected event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected stream event")
	}
}

func TestACPHandle_handleAgentRequest_permission(t *testing.T) {
	h := &acpHandle{
		events:    make(chan RuntimeEvent, 64),
		done:      make(chan struct{}),
		stdin:     &fakeWriteCloser{},
		sessionID: "sess_123",
	}

	msg := &jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      intPtr(42),
		Method:  "session/request_permission",
		Params: map[string]any{
			"requestId": "req_1",
			"toolCall": map[string]any{
				"name": "bash",
			},
		},
	}
	h.handleAgentRequest(msg)

	// Verify that a response was written to stdin.
	fwc := h.stdin.(*fakeWriteCloser)
	if len(fwc.written) == 0 {
		t.Fatal("expected permission response to be written")
	}
	last := fwc.written[len(fwc.written)-1]
	if !strContains(last, `"id":42`) || !strContains(last, `"outcome":"selected"`) || !strContains(last, `"optionId":"always"`) {
		t.Fatalf("unexpected permission response: %s", last)
	}
	select {
	case evt := <-h.events:
		if evt.Type != "tool_call" || !strContains(evt.Payload, "bash") {
			t.Fatalf("unexpected permission event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected tool_call event")
	}
}

func TestSelectedACPOptionIDPrefersAllowAlways(t *testing.T) {
	got := selectedACPOptionID(map[string]any{
		"options": []any{
			map[string]any{"optionId": "once", "kind": "allow_once"},
			map[string]any{"optionId": "always", "kind": "allow_always"},
			map[string]any{"optionId": "reject", "kind": "reject_once"},
		},
	})
	if got != "always" {
		t.Fatalf("expected always, got %q", got)
	}
}

func TestACPHandle_handleWriteTextFileHonorsWorkspacePermission(t *testing.T) {
	workspace := t.TempDir()
	h := &acpHandle{
		events: make(chan RuntimeEvent, 64),
		config: SessionConfig{
			WorkspacePath: workspace,
		},
	}
	if err := h.handleWriteTextFile(map[string]any{"path": "AI_ORCH_REVIEW_FINDINGS.md", "content": "findings"}); err != nil {
		t.Fatalf("handleWriteTextFile returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "AI_ORCH_REVIEW_FINDINGS.md"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "findings" {
		t.Fatalf("unexpected file content %q", string(data))
	}
	var sawPatch bool
	for len(h.events) > 0 {
		evt := <-h.events
		if evt.Type == "patch" && strContains(evt.Payload, "AI_ORCH_REVIEW_FINDINGS.md") {
			sawPatch = true
		}
	}
	if !sawPatch {
		t.Fatal("expected ACP write to emit patch evidence")
	}
}

func TestACPHandle_handleWriteTextFileRejectsWorkspaceEscape(t *testing.T) {
	h := &acpHandle{
		config: SessionConfig{
			WorkspacePath: t.TempDir(),
		},
	}
	if err := h.handleWriteTextFile(map[string]any{"path": "../outside.md", "content": "nope"}); err == nil {
		t.Fatal("expected workspace escape to be rejected")
	}
}

func TestACPHandle_handleWriteTextFileDoesNotUseAgentPermissionFlags(t *testing.T) {
	h := &acpHandle{
		config: SessionConfig{
			WorkspacePath: t.TempDir(),
			Permissions:   map[string]string{"workspace_write": "deny"},
		},
	}
	if err := h.handleWriteTextFile(map[string]any{"path": "x.md", "content": "ok"}); err != nil {
		t.Fatalf("ACP should not use agent permission flags for file writes: %v", err)
	}
}

func TestACPHandle_workspacePatchSinceCapturesDirectWorkspaceEdit(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	before, err := captureACPWorkspaceSnapshot(workspace)
	if err != nil {
		t.Fatalf("capture before snapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "AI_ORCH_REVIEW_FINDINGS.md"), []byte("findings\n"), 0o644); err != nil {
		t.Fatalf("write findings file: %v", err)
	}
	h := &acpHandle{config: SessionConfig{SessionID: "sess_acp"}, events: make(chan RuntimeEvent, 4)}
	patch := h.workspacePatchSince(workspace, before)
	if !strContains(patch, "AI_ORCH_REVIEW_FINDINGS.md") || !strContains(patch, `"action":"create"`) {
		t.Fatalf("expected create patch for findings file, got %s", patch)
	}
}

func TestACPHandle_handleNotification_routesToSessionUpdate(t *testing.T) {
	h := &acpHandle{
		events: make(chan RuntimeEvent, 64),
		done:   make(chan struct{}),
	}

	msg := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params: map[string]any{
			"sessionId": "sess_123",
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content": map[string]any{
					"type": "text",
					"text": "chunk",
				},
			},
		},
	}
	h.handleNotification(msg)

	select {
	case evt := <-h.events:
		if evt.Type != "stream" || evt.Payload != "chunk" {
			t.Fatalf("unexpected event: %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected stream event")
	}
}

func TestACPHandle_emitNonCriticalEventNonBlocking(t *testing.T) {
	h := &acpHandle{
		events: make(chan RuntimeEvent, 0), // unbuffered
		done:   make(chan struct{}),
	}
	h.emitEvent(RuntimeEvent{Type: "stream", Payload: "drop me"})
}

func TestACPHandle_emitErrorStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := &acpHandle{
		ctx:    ctx,
		events: make(chan RuntimeEvent, 0), // unbuffered
		done:   make(chan struct{}),
	}
	h.emitError("test error")
}

func TestExtractPatchFromText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "simple patch envelope",
			text: `Some text {"patch":"abc123","files":[{"file_path":"foo.go","diff":"+bar"}]} more text`,
			want: `{"patch":"abc123","files":[{"file_path":"foo.go","diff":"+bar"}]}`,
		},
		{
			name: "patchId envelope",
			text: `{"patchId":"abc123","files":[{"path":"foo.go","action":"modify","newContent":"bar"}]}`,
			want: `{"patchId":"abc123","files":[{"path":"foo.go","action":"modify","newContent":"bar"}]}`,
		},
		{
			name: "no patch",
			text: `{"foo":"bar"}`,
			want: "",
		},
		{
			name: "nested braces",
			text: `{"outer":{"patch":"id","files":[]}}`,
			want: `{"outer":{"patch":"id","files":[]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPatchFromText(tt.text)
			if got != tt.want {
				t.Fatalf("extractPatchFromText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractPatchFromResult(t *testing.T) {
	tests := []struct {
		name   string
		result map[string]any
		want   string
	}{
		{
			name:   "content field",
			result: map[string]any{"content": "patch payload"},
			want:   "patch payload",
		},
		{
			name:   "patch field",
			result: map[string]any{"patch": "patch payload"},
			want:   "patch payload",
		},
		{
			name:   "output field",
			result: map[string]any{"output": "patch payload"},
			want:   "patch payload",
		},
		{
			name:   "nil result",
			result: nil,
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPatchFromResult(tt.result)
			if got != tt.want {
				t.Fatalf("extractPatchFromResult() = %q, want %q", got, tt.want)
			}
		})
	}
}

type fakeWriteCloser struct {
	written []string
	closed  bool
}

func (f *fakeWriteCloser) Write(p []byte) (int, error) {
	f.written = append(f.written, string(p))
	return len(p), nil
}

func (f *fakeWriteCloser) Close() error {
	f.closed = true
	return nil
}

func intPtr(i int) *int {
	return &i
}

func strContains(s, substr string) bool {
	return len(s) >= len(substr) && strIndexOf(s, substr) >= 0
}

func strIndexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
