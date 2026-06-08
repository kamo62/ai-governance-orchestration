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
				"specialist": "unit-tests",
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
	if !strings.Contains(result.Content[0].Text, "unit-tests") {
		t.Fatalf("expected specialist in response, got: %s", result.Content[0].Text)
	}
}

func TestDelegateGovernedWorkEscapesSessionIDPathSegment(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/sessions/sess%2F123/messages" {
			t.Fatalf("expected escaped session path, got path=%q escaped=%q", r.URL.Path, r.URL.EscapedPath())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"specialist": "unit-tests",
			"reason":     "matched agent catalog",
			"status":     "routed",
		})
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	resp := s.Handle(context.Background(), &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "delegate_governed_work",
			"arguments": map[string]any{
				"session_id": "sess/123",
				"prompt":     "write tests",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
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

func TestRecordExternalToolCallPostsEvidenceRoute(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/evidence" {
			t.Fatalf("expected POST /v1/evidence, got %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode evidence body: %v", err)
		}
		if body["session_id"] != "sess_123" || body["trust_level"] != "self_reported" {
			t.Fatalf("unexpected evidence body: %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	resp := s.Handle(context.Background(), &Request{
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
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result := toolsCallResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
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

func TestRecordExternalModelCallPostsEvidenceRoute(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/evidence" {
			t.Fatalf("expected POST /v1/evidence, got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	resp := s.Handle(context.Background(), &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "record_external_model_call",
			"arguments": map[string]any{
				"session_id": "sess_123",
				"model":      "gpt-4",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result := toolsCallResult(t, resp.Result)
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}
}

func TestCreateContextManifest(t *testing.T) {
	var gotBody map[string]any
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/context-manifests" {
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode manifest body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "manifest_123"})
			return
		}
		http.NotFound(w, r)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	resp := s.Handle(context.Background(), &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "create_context_manifest",
			"arguments": map[string]any{
				"session_id":       "sess_123",
				"summary":          "test context",
				"source_system":    "repo",
				"source_object_id": "src/main.go",
				"classification":   "internal",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result := toolsCallResult(t, resp.Result)
	if !strings.Contains(result.Content[0].Text, "manifest_123") {
		t.Fatalf("expected manifest ID in response, got: %s", result.Content[0].Text)
	}
	id, ok := gotBody["id"].(string)
	if !ok || !strings.HasPrefix(id, "ctx_") || strings.Contains(id, "/") {
		t.Fatalf("expected safe hashed manifest id, got %#v", gotBody["id"])
	}
}

func TestAttachUseCase(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/use-cases" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "uc_123"})
			return
		}
		http.NotFound(w, r)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	resp := s.Handle(context.Background(), &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "attach_use_case",
			"arguments": map[string]any{
				"id":             "uc_123",
				"owner":          "team-a",
				"domain":         "platform",
				"classification": "internal",
				"risk_level":     "medium",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result := toolsCallResult(t, resp.Result)
	if !strings.Contains(result.Content[0].Text, "uc_123") {
		t.Fatalf("expected use case ID in response, got: %s", result.Content[0].Text)
	}
}

func TestAttachWorkflow(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/workflows" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "wf_123"})
			return
		}
		http.NotFound(w, r)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1ITools(s, cfg)

	resp := s.Handle(context.Background(), &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "attach_workflow",
			"arguments": map[string]any{
				"id":     "wf_123",
				"name":   "review",
				"stages": []string{"draft", "review", "merge"},
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result := toolsCallResult(t, resp.Result)
	if !strings.Contains(result.Content[0].Text, "wf_123") {
		t.Fatalf("expected workflow ID in response, got: %s", result.Content[0].Text)
	}
}

func TestListAllowedTools(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/internal/v1/mcp/catalog" {
			if got := r.Header.Get("X-AI-Orch-Session-ID"); got != "sess_123" {
				t.Fatalf("expected session header sess_123, got %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"servers": map[string]any{
					"repo-classification": map[string]any{
						"tools": []string{"getRepoClassification"},
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1JTools(s, cfg)

	resp := s.Handle(context.Background(), &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  mustRawMessage(map[string]any{"name": "list_allowed_tools", "arguments": map[string]any{"session_id": "sess_123"}}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result := toolsCallResult(t, resp.Result)
	if !strings.Contains(result.Content[0].Text, "getRepoClassification") {
		t.Fatalf("expected tool name in response, got: %s", result.Content[0].Text)
	}
}

func TestCallGovernedTool(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/internal/v1/mcp/repo-classification/tools/getRepoClassification" {
			if got := r.Header.Get("X-AI-Orch-Session-ID"); got != "sess_123" {
				t.Fatalf("expected session header sess_123, got %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"classification": "internal"})
			return
		}
		http.NotFound(w, r)
	}))
	defer mock.Close()

	cfg := &GatewayConfig{GovernanceURL: mock.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1JTools(s, cfg)

	resp := s.Handle(context.Background(), &Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "call_governed_tool",
			"arguments": map[string]any{
				"server_id":  "repo-classification",
				"tool_name":  "getRepoClassification",
				"arguments":  map[string]any{"repo_url": "https://example.com/repo"},
				"session_id": "sess_123",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result := toolsCallResult(t, resp.Result)
	if !strings.Contains(result.Content[0].Text, "internal") {
		t.Fatalf("expected upstream response in result, got: %s", result.Content[0].Text)
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
