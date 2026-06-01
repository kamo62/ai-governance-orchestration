package dispatch

import (
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
	if !strContains(last, `"id":42`) || !strContains(last, `"outcome":"denied"`) {
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

func TestACPHandle_emitError_nonBlocking(t *testing.T) {
	h := &acpHandle{
		events: make(chan RuntimeEvent, 0), // unbuffered
		done:   make(chan struct{}),
	}
	// Should not block even when channel is full.
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
