package mcpgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDelegateGovernedWork(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"specialist": "test-generation",
				"reason":     "matched agent catalog",
				"status":     "routed",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "delegate_governed_work",
			"arguments": map[string]any{
				"session_id": "sess_123",
				"prompt":     "write tests",
			},
		}),
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result := toolsCallResult(t, resp.Result)
	if !strings.Contains(result.Content[0].Text, "test-generation") {
		t.Fatalf("expected specialist in response, got: %s", result.Content[0].Text)
	}
}

func TestRecordPatchDecision(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/patch-decision") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "record_patch_decision",
			"arguments": map[string]any{
				"session_id": "sess_123",
				"patch_id":   "patch_1",
				"decision":   "applied",
			},
		}),
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result := toolsCallResult(t, resp.Result)
	if !strings.Contains(result.Content[0].Text, "applied") {
		t.Fatalf("expected applied in response, got: %s", result.Content[0].Text)
	}
}

func TestLookupAudit(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/audit/") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"events": []map[string]any{
					{"event_type": "session.created", "actor": "dev"},
					{"event_type": "patch.proposed", "actor": "dev"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name":      "lookup_audit",
			"arguments": map[string]any{"session_id": "sess_123"},
		}),
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result := toolsCallResult(t, resp.Result)
	if !strings.Contains(result.Content[0].Text, "2 event(s)") {
		t.Fatalf("expected 2 events in response, got: %s", result.Content[0].Text)
	}
}

func TestRecordExternalToolCall(t *testing.T) {
	cfg := &GatewayConfig{GovernanceURL: "http://invalid", DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "record_external_tool_call",
			"arguments": map[string]any{
				"session_id": "sess_123",
				"tool_name":  "bash",
				"outcome":    "success",
			},
		}),
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result := toolsCallResult(t, resp.Result)
	if !result.IsError {
		t.Fatalf("expected failed persistence to be reported as tool error, got: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "record self-reported tool call failed") {
		t.Fatalf("expected persistence failure in response, got: %s", result.Content[0].Text)
	}
}

func TestRecordExternalModelCall(t *testing.T) {
	cfg := &GatewayConfig{GovernanceURL: "http://invalid", DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	req := &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "record_external_model_call",
			"arguments": map[string]any{
				"session_id": "sess_123",
				"model":      "gpt-4",
				"cost_usd":   0.01,
			},
		}),
	}

	resp := s.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result := toolsCallResult(t, resp.Result)
	if !result.IsError {
		t.Fatalf("expected failed persistence to be reported as tool error, got: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "record self-reported model call failed") {
		t.Fatalf("expected persistence failure in response, got: %s", result.Content[0].Text)
	}
}

func toolsCallResult(t *testing.T, result any) ToolsCallResult {
	t.Helper()
	switch r := result.(type) {
	case ToolsCallResult:
		return r
	case *ToolsCallResult:
		return *r
	default:
		t.Fatalf("expected ToolsCallResult, got %T", result)
		return ToolsCallResult{}
	}
}
