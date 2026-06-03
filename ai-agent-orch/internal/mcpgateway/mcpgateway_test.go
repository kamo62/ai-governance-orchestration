package mcpgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerHandleInitialize(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: mustRawMessage(map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "test-client",
				"version": "1.0.0",
			},
		}),
	}

	resp := s.Handle(context.Background(), req)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(InitializeResult)
	if !ok {
		t.Fatalf("expected InitializeResult, got %T", resp.Result)
	}
	if result.ProtocolVersion != ProtocolVersion {
		t.Fatalf("expected protocol version %s, got %s", ProtocolVersion, result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "test-server" {
		t.Fatalf("expected server name test-server, got %s", result.ServerInfo.Name)
	}
}

func TestServerHandleInitializeInvalidVersion(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  mustRawMessage(map[string]any{"protocolVersion": "invalid"}),
	}

	resp := s.Handle(context.Background(), req)
	if resp == nil {
		t.Fatal("expected response")
	}
	// The server does not validate protocol version strings; any string is accepted.
	if resp.Error != nil {
		t.Fatalf("did not expect error for arbitrary protocol version: %+v", resp.Error)
	}
}

func TestServerHandleToolsList(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	s.RegisterTool(Tool{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: mustJSONSchema(map[string]any{"type": "object"}),
	}, func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		return &ToolsCallResult{Content: []ContentItem{TextContent("ok")}}, nil
	})

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(ToolsListResult)
	if !ok {
		t.Fatalf("expected ToolsListResult, got %T", resp.Result)
	}
	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "test_tool" {
		t.Fatalf("expected tool name test_tool, got %s", result.Tools[0].Name)
	}
}

func TestServerHandleToolsCall(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	s.RegisterTool(Tool{
		Name:        "echo",
		Description: "Echoes the input",
		InputSchema: mustJSONSchema(map[string]any{"type": "object"}),
	}, func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		var params map[string]string
		_ = json.Unmarshal(args, &params)
		return &ToolsCallResult{Content: []ContentItem{TextContent(params["msg"])}}, nil
	})

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  mustRawMessage(map[string]any{"name": "echo", "arguments": map[string]string{"msg": "hello"}}),
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(*ToolsCallResult)
	if !ok {
		t.Fatalf("expected *ToolsCallResult, got %T", resp.Result)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "hello" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestServerHandleToolsCallUnknownTool(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  mustRawMessage(map[string]any{"name": "unknown"}),
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != ErrInvalidParams {
		t.Fatalf("expected invalid params error, got %d", resp.Error.Code)
	}
}

func TestServerHandleUnknownMethod(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "unknown/method",
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != ErrMethodNotFound {
		t.Fatalf("expected method not found, got %d", resp.Error.Code)
	}
}

func TestServerHandleNotification(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	req := &Request{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	resp := s.Handle(context.Background(), req)
	if resp != nil {
		t.Fatal("notifications should not return a response")
	}
}

func TestHTTPServerHealthz(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	hs := NewHTTPServer(s, "")
	mux := http.NewServeMux()
	hs.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/mcp/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "ok" {
		t.Fatalf("unexpected status: %s", result["status"])
	}
}

func TestHTTPServerAuth(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	hs := NewHTTPServer(s, "secret-token")
	mux := http.NewServeMux()
	hs.RegisterRoutes(mux)

	// Missing auth.
	req := httptest.NewRequest(http.MethodGet, "/mcp/v1/sse", nil)
	req.Host = "127.0.0.1:18081"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", w.Code)
	}

	// Wrong auth.
	req = httptest.NewRequest(http.MethodGet, "/mcp/v1/sse", nil)
	req.Host = "127.0.0.1:18081"
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong auth, got %d", w.Code)
	}
}

func TestHTTPServerFailsClosedWhenTokenMissing(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	hs := NewHTTPServer(s, "")
	mux := http.NewServeMux()
	hs.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/mcp/v1/messages", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Host = "127.0.0.1:18081"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when token is missing, got %d", w.Code)
	}
}

func TestHTTPServerRejectsNonLocalOrigin(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	hs := NewHTTPServer(s, "secret-token")
	mux := http.NewServeMux()
	hs.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/mcp/v1/messages", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Host = "127.0.0.1:18081"
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-local origin, got %d", w.Code)
	}
}

func TestHTTPServerAllowsLocalOrigin(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	hs := NewHTTPServer(s, "secret-token")
	mux := http.NewServeMux()
	hs.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/mcp/v1/messages", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Host = "127.0.0.1:18081"
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Origin", "http://127.0.0.1:18081")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for local origin, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:18081" {
		t.Fatalf("expected local CORS echo, got %q", got)
	}
}

func TestHTTPServerRejectsNonLocalHost(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	hs := NewHTTPServer(s, "secret-token")
	mux := http.NewServeMux()
	hs.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/mcp/v1/messages", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Host = "example.com"
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-local host, got %d", w.Code)
	}
}

func TestStdioTransport(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	s.RegisterTool(Tool{
		Name:        "hello",
		Description: "Says hello",
		InputSchema: mustJSONSchema(map[string]any{"type": "object"}),
	}, func(ctx context.Context, args json.RawMessage) (*ToolsCallResult, error) {
		return &ToolsCallResult{Content: []ContentItem{TextContent("world")}}, nil
	})

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}` + "\n")
	var out strings.Builder
	trans := NewReadWriteTransport(s, in, &out)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run in background since stdio transport blocks.
	done := make(chan error, 1)
	go func() {
		done <- trans.Run(ctx)
	}()

	// Wait for processing.
	select {
	case err := <-done:
		if err != nil && err != context.DeadlineExceeded {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
	}

	output := out.String()
	if !strings.Contains(output, `"result"`) {
		t.Fatalf("expected result in output, got: %s", output)
	}
}

func TestStdioTransportProcessesLargeFinalLineWithoutTrailingNewline(t *testing.T) {
	s := NewServer("test-server", "1.0.0")
	largeParam := strings.Repeat("x", 80*1024)
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"` + largeParam + `","version":"1.0.0"}}}`)
	var out strings.Builder
	trans := NewReadWriteTransport(s, in, &out)

	if err := trans.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), `"result"`) {
		t.Fatalf("expected result for large final line, got: %s", out.String())
	}
}

func TestGatewayConfigDoctor(t *testing.T) {
	// Start a mock governance server.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()

	cfg := &GatewayConfig{
		GovernanceURL: mock.URL,
		DevToken:      "test-token",
	}
	s := NewServer("test", "1.0")
	RegisterPhase1GTools(s, cfg)

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  mustRawMessage(map[string]any{"name": "mcp_doctor", "arguments": map[string]any{}}),
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(*ToolsCallResult)
	if !ok {
		t.Fatalf("expected *ToolsCallResult, got %T", resp.Result)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "OK") {
		t.Fatalf("expected OK in doctor report, got: %s", text)
	}
}

func TestGatewayConfigStartSession(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sessions" {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"session_id": "sess_123", "status": "created"})
			return
		}
		http.NotFound(w, r)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{
		GovernanceURL: mock.URL,
		DevToken:      "test-token",
	}
	s := NewServer("test", "1.0")
	RegisterPhase1GTools(s, cfg)

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "start_governed_session",
			"arguments": map[string]any{
				"agent":          "test-generation",
				"classification": "internal",
				"prompt":         "hello",
			},
		}),
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(*ToolsCallResult)
	if !ok {
		t.Fatalf("expected *ToolsCallResult, got %T", resp.Result)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
	if !strings.Contains(result.Content[0].Text, "sess_123") {
		t.Fatalf("expected session ID in response, got: %s", result.Content[0].Text)
	}
}

func mustRawMessage(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
