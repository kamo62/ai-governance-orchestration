package classifier

import (
	"context"
	"testing"
)

func TestClassifierClassifiesTestGeneration(t *testing.T) {
	c := New(nil)
	result, err := c.Classify(context.Background(), Request{
		Prompt:         "Write Playwright tests for the login page",
		Classification: "internal",
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.TaskType != "test_generation" {
		t.Fatalf("expected task_type test_generation, got %q", result.TaskType)
	}
	if result.AgentProfile != "unit-tests" {
		t.Fatalf("expected agent_profile unit-tests, got %q", result.AgentProfile)
	}
	if result.UseCase != "quality_assurance" {
		t.Fatalf("expected use_case quality_assurance, got %q", result.UseCase)
	}
	if len(result.EvidenceRequirements) == 0 {
		t.Fatal("expected evidence requirements")
	}
	if result.ModelRoute != "coding-balanced" {
		t.Fatalf("expected model_route coding-balanced, got %q", result.ModelRoute)
	}
}

func TestClassifierClassifiesSecurityScan(t *testing.T) {
	c := New(nil)
	result, err := c.Classify(context.Background(), Request{
		Prompt:         "Check this code for secret exposure and auth issues",
		Classification: "confidential",
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.TaskType != "security_scan" {
		t.Fatalf("expected task_type security_scan, got %q", result.TaskType)
	}
	if result.AgentProfile != "security-scan" {
		t.Fatalf("expected agent_profile security-scan, got %q", result.AgentProfile)
	}
	if result.RiskLevel != "high" {
		t.Fatalf("expected risk_level high, got %q", result.RiskLevel)
	}
	if result.ModelRoute != "coding-primary" {
		t.Fatalf("expected model_route coding-primary for security, got %q", result.ModelRoute)
	}
}

func TestClassifierClassifiesBackendDevelopment(t *testing.T) {
	c := New(nil)
	result, err := c.Classify(context.Background(), Request{
		Prompt:         "Create a Go HTTP handler for a REST API endpoint",
		Classification: "internal",
		Branch:         "feature/API-123-endpoint",
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.TaskType != "backend_development" {
		t.Fatalf("expected task_type backend_development, got %q", result.TaskType)
	}
	if result.AgentProfile != "backend-development" {
		t.Fatalf("expected agent_profile backend-development, got %q", result.AgentProfile)
	}
}

func TestClassifierClassifiesFrontendDevelopment(t *testing.T) {
	c := New(nil)
	result, err := c.Classify(context.Background(), Request{
		Prompt:         "Build a React component with TypeScript and CSS",
		Classification: "internal",
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.TaskType != "frontend_development" {
		t.Fatalf("expected task_type frontend_development, got %q", result.TaskType)
	}
	if result.AgentProfile != "frontend-development" {
		t.Fatalf("expected agent_profile frontend-development, got %q", result.AgentProfile)
	}
}

func TestClassifierRecordsLLMSuggestion(t *testing.T) {
	mockLLM := &mockAssistant{
		suggestion: Suggestion{
			TaskType:     "review",
			AgentProfile: "code-review",
			Confidence:   0.9,
			Reason:       "looks like a review task",
		},
	}
	c := New(mockLLM)
	result, err := c.Classify(context.Background(), Request{
		Prompt:         "Review this diff for bugs",
		Classification: "internal",
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if result.TaskType != "review" {
		t.Fatalf("expected task_type review, got %q", result.TaskType)
	}
	// LLM suggestion should be recorded in reasons.
	found := false
	for _, r := range result.Reasons {
		if contains(r, "llm_suggestion") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected llm_suggestion in reasons: %v", result.Reasons)
	}
}

func TestClassifierLLMDisagreementRecorded(t *testing.T) {
	mockLLM := &mockAssistant{
		suggestion: Suggestion{
			TaskType:     "test_generation",
			AgentProfile: "unit-tests",
			Confidence:   0.95,
			Reason:       "definitely tests",
		},
	}
	c := New(mockLLM)
	result, err := c.Classify(context.Background(), Request{
		Prompt:         "Refactor this module without changing behavior",
		Classification: "internal",
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	// LLM thinks it's tests, deterministic says refactor.
	found := false
	for _, r := range result.Reasons {
		if contains(r, "llm_disagreement") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected llm_disagreement in reasons: %v", result.Reasons)
	}
}

type mockAssistant struct {
	suggestion Suggestion
}

func (m *mockAssistant) Suggest(ctx context.Context, req Request) (Suggestion, error) {
	return m.suggestion, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
