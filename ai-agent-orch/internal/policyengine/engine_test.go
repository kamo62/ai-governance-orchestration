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
