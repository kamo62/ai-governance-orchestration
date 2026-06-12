package policyengine

import (
	"context"
	"strings"
	"testing"
)

func TestNewSelectsNativeAndRejectsUnimplementedAGT(t *testing.T) {
	engine, err := New("native")
	if err != nil {
		t.Fatalf("native engine returned error: %v", err)
	}
	if engine.Name() != "native" {
		t.Fatalf("expected native engine, got %q", engine.Name())
	}

	_, err = New("agt")
	if err == nil {
		t.Fatal("expected agt engine to fail closed until adapter is implemented")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not implemented error, got %v", err)
	}
}

func TestNativeEngineDeniesSecretFindings(t *testing.T) {
	engine, err := New("native")
	if err != nil {
		t.Fatalf("native engine returned error: %v", err)
	}

	decision, err := engine.Evaluate(context.Background(), Request{
		SessionID:  "sess_test",
		AgentName:  "unit-tests",
		ActionType: "session.create",
		Findings:   []string{"openrouter_api_key"},
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected secret finding to be denied")
	}
	if decision.Engine != "native" {
		t.Fatalf("expected native decision, got %q", decision.Engine)
	}
	if decision.DecisionID == "" {
		t.Fatal("expected decision id")
	}
}

func TestClassificationExceedsMaxBoundaries(t *testing.T) {
	tests := []struct {
		name           string
		classification string
		max            string
		wantExceeds    bool
		wantErr        string
	}{
		{name: "equal public", classification: "public", max: "public", wantExceeds: false},
		{name: "internal below restricted", classification: "internal", max: "restricted", wantExceeds: false},
		{name: "confidential exceeds internal", classification: "confidential", max: "internal", wantExceeds: true},
		{name: "blank max defaults internal", classification: "confidential", max: "", wantExceeds: true},
		{name: "unknown classification", classification: "secret", max: "internal", wantErr: `unknown classification "secret"`},
		{name: "unknown max", classification: "internal", max: "secret", wantErr: `unknown max classification "secret"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := classificationExceedsMax(tt.classification, tt.max)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("expected error %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantExceeds {
				t.Fatalf("expected exceeds=%v, got %v", tt.wantExceeds, got)
			}
		})
	}
}

func TestNativeEngineAppliesDefaultClassificationMax(t *testing.T) {
	decision, err := NativeEngine{}.Evaluate(context.Background(), Request{
		Classification: "confidential",
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected confidential to exceed default internal ceiling")
	}
	if !strings.Contains(decision.Reason, "exceeds") {
		t.Fatalf("expected ceiling reason, got %q", decision.Reason)
	}
}

func TestNativeEngineMCPToolCallPolicy(t *testing.T) {
	tests := []struct {
		name      string
		req       Request
		wantAllow bool
		wantPart  string
	}{
		{
			name: "allowed tool for allowed agent",
			req: Request{
				ActionType:     "mcp.tool_call",
				Resource:       "documentation",
				ToolName:       "getPage",
				AgentName:      "documentation",
				Classification: "internal",
				Metadata: map[string]any{
					"allowed_agents": []string{"documentation"},
					"tool_allow":     []string{"getPage", "searchPages"},
					"tool_deny":      []string{"deletePage"},
				},
			},
			wantAllow: true,
			wantPart:  "allowed",
		},
		{
			name: "denies tool outside allow list",
			req: Request{
				ActionType:     "mcp.tool_call",
				Resource:       "documentation",
				ToolName:       "deletePage",
				AgentName:      "documentation",
				Classification: "internal",
				Metadata: map[string]any{
					"allowed_agents": []string{"documentation"},
					"tool_allow":     []string{"getPage"},
				},
			},
			wantPart: "not in allow list",
		},
		{
			name: "denies disallowed agent",
			req: Request{
				ActionType:     "mcp.tool_call",
				Resource:       "playwright-cli",
				ToolName:       "runPlaywrightTest",
				AgentName:      "code-review",
				Classification: "internal",
				Metadata: map[string]any{
					"allowed_agents": []string{"unit-tests"},
					"tool_allow":     []string{"runPlaywrightTest"},
				},
			},
			wantPart: "not allowed",
		},
		{
			name: "defaults deny without allow list",
			req: Request{
				ActionType:     "mcp.tool_call",
				Resource:       "documentation",
				ToolName:       "getPage",
				AgentName:      "documentation",
				Classification: "internal",
			},
			wantPart: "not in allow list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := NativeEngine{}.Evaluate(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Evaluate returned error: %v", err)
			}
			if decision.Allowed != tt.wantAllow {
				t.Fatalf("expected allowed=%v, got %v: %s", tt.wantAllow, decision.Allowed, decision.Reason)
			}
			if !strings.Contains(decision.Reason, tt.wantPart) {
				t.Fatalf("expected reason containing %q, got %q", tt.wantPart, decision.Reason)
			}
			if decision.DecisionID == "" {
				t.Fatal("expected decision id")
			}
		})
	}
}

func TestDetectSecretsPositiveAndNegativePatterns(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		finding string
	}{
		{name: "openrouter", text: "OPENROUTER_API_KEY=sk-or-v1-abcdefghijklmnopqrstuvwxyz", finding: "openrouter_api_key"},
		{name: "openai", text: "sk-abcdefghijklmnopqrstuvwxyz123456", finding: "openai_api_key"},
		{name: "aws access key", text: "AKIA1234567890ABCDEF", finding: "aws_access_key_id"},
		{name: "private key", text: "-----BEGIN PRIVATE KEY-----", finding: "private_key"},
		{name: "password assignment", text: "password=correcthorsebatterystaple", finding: "password_assignment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := DetectSecrets(tt.text)
			if !containsString(findings, tt.finding) {
				t.Fatalf("expected finding %q in %#v", tt.finding, findings)
			}
		})
	}

	negative := "safe prompt mentioning password policy, AKIA examples, and sk placeholder only"
	if findings := DetectSecrets(negative); len(findings) != 0 {
		t.Fatalf("expected no findings, got %#v", findings)
	}
}

func TestCommandAllowlistPermissionsAndSubcommands(t *testing.T) {
	list := &CommandAllowlist{SystemCommands: []SystemCommand{
		{
			Name:     "read_file",
			Default:  "allow",
			Requires: "",
		},
		{
			Name:     "write_file",
			Default:  "deny",
			Requires: "workspace_write=allow",
		},
		{
			Name:    "run_command",
			Default: "deny",
			Subcommands: []Subcommand{
				{Name: "playwright", AllowedAgents: []string{"unit-tests"}},
			},
		},
	}}

	if !list.IsAllowed("read_file", "", "code-review") {
		t.Fatal("expected default allow command to pass")
	}
	if list.IsAllowedWithPermissions("write_file", "", "documentation", map[string]string{"workspace_write": "deny"}) {
		t.Fatal("expected write_file to require workspace_write=allow")
	}
	if !list.IsAllowedWithPermissions("write_file", "", "documentation", map[string]string{"workspace_write": "allow"}) {
		t.Fatal("expected write_file to pass with workspace_write=allow")
	}
	if !list.IsAllowed("run_command", "playwright", "unit-tests") {
		t.Fatal("expected unit-tests to run playwright")
	}
	if list.IsAllowed("run_command", "playwright", "code-review") {
		t.Fatal("expected code-review to be denied playwright")
	}
	if list.IsAllowed("unknown", "", "unit-tests") {
		t.Fatal("expected unknown command to fail closed")
	}
}

func TestRequirementSatisfied(t *testing.T) {
	if !requirementSatisfied("workspace_write=allow", map[string]string{"workspace_write": "allow"}) {
		t.Fatal("expected matching requirement to pass")
	}
	if requirementSatisfied("workspace_write=allow", map[string]string{"workspace_write": "deny"}) {
		t.Fatal("expected mismatched requirement to fail")
	}
	if requirementSatisfied("", map[string]string{"workspace_write": "allow"}) {
		t.Fatal("expected blank requirement to fail")
	}
	if requirementSatisfied("workspace_write", map[string]string{"workspace_write": "allow"}) {
		t.Fatal("expected malformed requirement to fail")
	}
}

func TestToolLoopCounterObserve(t *testing.T) {
	counter := NewToolLoopCounter(2)
	if counter.Observe("tool_call") {
		t.Fatal("first tool call should not exceed")
	}
	if counter.Observe("mcp_call") {
		t.Fatal("second tool call should not exceed")
	}
	if !counter.Observe("command") {
		t.Fatal("third consecutive tool call should exceed")
	}
	if counter.Observe("patch") {
		t.Fatal("patch output should reset without exceeding")
	}
	if counter.Observe("tool_call") {
		t.Fatal("first tool call after reset should not exceed")
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
