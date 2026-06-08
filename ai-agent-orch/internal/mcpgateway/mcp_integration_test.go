package mcpgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPhase1EndToEnd exercises the full governance loop through the MCP gateway.
func TestPhase1EndToEnd(t *testing.T) {
	var auditEvents []map[string]any
	var sessionID string

	mockGov := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/sessions" && r.Method == http.MethodPost:
			sessionID = "sess_e2e_123"
			_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sessionID, "status": "created"})
		case strings.HasSuffix(r.URL.Path, "/messages") && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"specialist": "unit-tests", "reason": "matched", "status": "routed"})
		case strings.HasSuffix(r.URL.Path, "/patch-decision") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/audit/") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"events": auditEvents})
		case r.URL.Path == "/v1/context-manifests" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "manifest_123"})
		case r.URL.Path == "/v1/use-cases" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "uc_123"})
		case r.URL.Path == "/v1/workflows" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "wf_123"})
		case r.URL.Path == "/internal/v1/mcp/catalog" && r.Method == http.MethodGet:
			if got := r.Header.Get("X-AI-Orch-Session-ID"); got != sessionID {
				http.Error(w, "missing session header", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"servers": map[string]any{
					"repo-classification": map[string]any{"tools": []string{"getRepoClassification"}},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/internal/v1/mcp/") && r.Method == http.MethodPost:
			if got := r.Header.Get("X-AI-Orch-Session-ID"); got != sessionID {
				http.Error(w, "missing session header", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"classification": "internal"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockGov.Close()

	cfg := &GatewayConfig{GovernanceURL: mockGov.URL, DevToken: "tok"}
	s := NewServer("test", "1.0")
	RegisterPhase1GTools(s, cfg)
	RegisterPhase1ITools(s, cfg)
	RegisterPhase1JTools(s, cfg)

	ctx := context.Background()

	// 1. mcp_doctor
	resp := s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 1, Method: "tools/call",
		Params: mustRawMessage(map[string]any{"name": "mcp_doctor", "arguments": map[string]any{}}),
	})
	if resp.Error != nil {
		t.Fatalf("mcp_doctor failed: %+v", resp.Error)
	}

	// 2. start_governed_session
	resp = s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 2, Method: "tools/call",
		Params: mustRawMessage(map[string]any{
			"name":      "start_governed_session",
			"arguments": map[string]any{"agent": "unit-tests", "classification": "internal", "prompt": "hello"},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("start_governed_session failed: %+v", resp.Error)
	}

	// 3. create_context_manifest
	resp = s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 3, Method: "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "create_context_manifest",
			"arguments": map[string]any{
				"session_id": sessionID, "summary": "ctx", "source_system": "repo",
				"source_object_id": "main.go", "classification": "internal",
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("create_context_manifest failed: %+v", resp.Error)
	}

	// 4. attach_use_case
	resp = s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 4, Method: "tools/call",
		Params: mustRawMessage(map[string]any{
			"name":      "attach_use_case",
			"arguments": map[string]any{"id": "uc_123", "owner": "team", "domain": "platform", "classification": "internal", "risk_level": "medium"},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("attach_use_case failed: %+v", resp.Error)
	}

	// 5. attach_workflow
	resp = s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 5, Method: "tools/call",
		Params: mustRawMessage(map[string]any{
			"name":      "attach_workflow",
			"arguments": map[string]any{"id": "wf_123", "name": "review", "stages": []string{"draft", "review"}},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("attach_workflow failed: %+v", resp.Error)
	}

	// 6. delegate_governed_work
	resp = s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 6, Method: "tools/call",
		Params: mustRawMessage(map[string]any{
			"name":      "delegate_governed_work",
			"arguments": map[string]any{"session_id": sessionID, "prompt": "write tests", "context_manifest_id": "manifest_123"},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("delegate_governed_work failed: %+v", resp.Error)
	}

	// 7. list_allowed_tools
	resp = s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 7, Method: "tools/call",
		Params: mustRawMessage(map[string]any{"name": "list_allowed_tools", "arguments": map[string]any{"session_id": sessionID}}),
	})
	if resp.Error != nil {
		t.Fatalf("list_allowed_tools failed: %+v", resp.Error)
	}

	// 8. call_governed_tool
	resp = s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 8, Method: "tools/call",
		Params: mustRawMessage(map[string]any{
			"name": "call_governed_tool",
			"arguments": map[string]any{
				"server_id": "repo-classification", "tool_name": "getRepoClassification",
				"arguments": map[string]any{"repo_url": "https://example.com/repo"}, "session_id": sessionID,
			},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("call_governed_tool failed: %+v", resp.Error)
	}

	// 9. record_patch_decision
	resp = s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 9, Method: "tools/call",
		Params: mustRawMessage(map[string]any{
			"name":      "record_patch_decision",
			"arguments": map[string]any{"session_id": sessionID, "patch_id": "patch_1", "decision": "applied"},
		}),
	})
	if resp.Error != nil {
		t.Fatalf("record_patch_decision failed: %+v", resp.Error)
	}

	// 10. lookup_audit
	resp = s.Handle(ctx, &Request{
		JSONRPC: "2.0", ID: 10, Method: "tools/call",
		Params: mustRawMessage(map[string]any{"name": "lookup_audit", "arguments": map[string]any{"session_id": sessionID}}),
	})
	if resp.Error != nil {
		t.Fatalf("lookup_audit failed: %+v", resp.Error)
	}
}
