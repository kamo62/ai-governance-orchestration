package policyengine

import (
	"context"
	"os"
	"path/filepath"
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
		AgentName:  "test-generation",
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

func TestNativeEngineLoadsClassificationRoutingPolicy(t *testing.T) {
	dir := writePolicyDir(t, map[string]string{
		"classification-routing.yaml": `
classifications:
  - name: public
    rank: 0
  - name: internal
    rank: 1
  - name: restricted
    rank: 2
`,
	})
	engine, err := NewNativeEngine(NativeConfig{
		PolicyDir:         dir,
		ClassificationMax: "internal",
	})
	if err != nil {
		t.Fatalf("NewNativeEngine returned error: %v", err)
	}

	decision, err := engine.Evaluate(context.Background(), Request{
		ActionType:     "session.create",
		Classification: "restricted",
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected restricted classification to be denied")
	}
	if decision.Category != "classification" {
		t.Fatalf("expected classification category, got %q", decision.Category)
	}
	if decision.Reason != "classification restricted exceeds max internal" {
		t.Fatalf("unexpected reason: %q", decision.Reason)
	}
}

func TestNativeEngineLoadsSecretPatternsPolicy(t *testing.T) {
	dir := writePolicyDir(t, map[string]string{
		"secrets-patterns.yaml": `
patterns:
  - name: custom_yaml_secret
    regex: 'CUSTOM-[0-9]{4}'
    severity: critical
`,
	})
	engine, err := NewNativeEngine(NativeConfig{PolicyDir: dir})
	if err != nil {
		t.Fatalf("NewNativeEngine returned error: %v", err)
	}

	decision, err := engine.Evaluate(context.Background(), Request{
		ActionType:     "session.create",
		Classification: "internal",
		Prompt:         "please use CUSTOM-1234 for this run",
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected YAML secret pattern to deny")
	}
	if decision.Category != "secret" {
		t.Fatalf("expected secret category, got %q", decision.Category)
	}
	if got := strings.Join(decision.Findings, ","); got != "custom_yaml_secret" {
		t.Fatalf("expected custom secret finding, got %q", got)
	}
}

func TestNativeEngineLoadsCostCapsPolicy(t *testing.T) {
	dir := writePolicyDir(t, map[string]string{
		"cost-caps.yaml": `
defaults:
  specialist_cap_usd: 0.42
`,
	})
	engine, err := NewNativeEngine(NativeConfig{
		PolicyDir:      dir,
		CostCapEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewNativeEngine returned error: %v", err)
	}

	decision, err := engine.Evaluate(context.Background(), Request{
		ActionType:       "session.create",
		Classification:   "internal",
		EstimatedCostUSD: 0.50,
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("expected cost cap to deny")
	}
	if decision.Category != "cost" {
		t.Fatalf("expected cost category, got %q", decision.Category)
	}
	if decision.CostCapUSD != 0.42 {
		t.Fatalf("expected cost cap 0.42, got %.2f", decision.CostCapUSD)
	}
}

func TestNativeEngineLoadsSDLCWorkflowPolicy(t *testing.T) {
	dir := writePolicyDir(t, map[string]string{
		"sdlc-governance.yaml": `
workflows:
  - id: change-release
    requires_approval: true
    required_evidence:
      - test_result
      - patch_decision
`,
	})
	engine, err := NewNativeEngine(NativeConfig{PolicyDir: dir})
	if err != nil {
		t.Fatalf("NewNativeEngine returned error: %v", err)
	}

	decision, err := engine.Evaluate(context.Background(), Request{
		ActionType:     "session.create",
		Classification: "internal",
		WorkflowID:     "change-release",
	})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("expected SDLC evidence policy to allow session creation, got deny: %s", decision.Reason)
	}
	if !decision.RequiresApproval {
		t.Fatal("expected workflow policy to require approval")
	}
	if got := strings.Join(decision.RequiredEvidence, ","); got != "test_result,patch_decision" {
		t.Fatalf("unexpected required evidence: %q", got)
	}
}

func writePolicyDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}
