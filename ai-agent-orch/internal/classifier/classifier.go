// Package classifier implements the Governance Router classification service.
// It classifies prompt + repo metadata into use case, workflow, agent profile,
// model route and evidence requirements. Deterministic policy owns the final
// decision; an optional LLM assistant may provide suggestions only.
package classifier

import (
	"context"
	"fmt"
	"strings"
)

// Request holds the input for classification.
type Request struct {
	Prompt         string `json:"prompt"`
	RepoURL        string `json:"repo_url,omitempty"`
	Branch         string `json:"branch,omitempty"`
	CommitSHA      string `json:"commit_sha,omitempty"`
	WorkItemID     string `json:"work_item_id,omitempty"`
	WorkItemType   string `json:"work_item_type,omitempty"`
	ActorHint      string `json:"actor_hint,omitempty"`
	SourceSystem   string `json:"source_system,omitempty"`
	Classification string `json:"classification,omitempty"`
}

// Result is the output of a classification decision.
type Result struct {
	TaskType             string   `json:"task_type"`
	UseCase              string   `json:"use_case"`
	Workflow             string   `json:"workflow"`
	AgentProfile         string   `json:"agent_profile"`
	ModelRoute           string   `json:"model_route"`
	EvidenceRequirements []string `json:"evidence_requirements"`
	Reasons              []string `json:"reasons"`
	CostPosture          string   `json:"cost_posture"`
	LatencyPosture       string   `json:"latency_posture"`
	RiskLevel            string   `json:"risk_level"`
}

// LLMAssistant is an optional interface for LLM-based classification suggestions.
// The deterministic classifier always overrides LLM suggestions.
type LLMAssistant interface {
	Suggest(ctx context.Context, req Request) (Suggestion, error)
}

// Suggestion is a non-binding recommendation from an LLM assistant.
type Suggestion struct {
	TaskType     string  `json:"task_type"`
	AgentProfile string  `json:"agent_profile"`
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
}

// Classifier implements deterministic governance classification.
type Classifier struct {
	llm LLMAssistant
}

// New creates a Classifier. llm may be nil.
func New(llm LLMAssistant) *Classifier {
	return &Classifier{llm: llm}
}

// Classify applies deterministic rules to produce a classification result.
// If an LLM assistant is configured, its suggestion is recorded in reasons
// but never overrides the deterministic decision.
func (c *Classifier) Classify(ctx context.Context, req Request) (Result, error) {
	result := Result{
		Reasons:        []string{},
		CostPosture:    "controlled",
		LatencyPosture: "balanced",
		RiskLevel:      "medium",
	}

	text := strings.ToLower(req.Prompt)
	branch := strings.ToLower(req.Branch)

	// Step 1: Determine task type from prompt keywords.
	result.TaskType = classifyTaskType(text)
	result.Reasons = append(result.Reasons, fmt.Sprintf("task_type: %s (keyword match)", result.TaskType))

	// Step 2: Determine use case from task type + branch hints.
	result.UseCase = classifyUseCase(result.TaskType, branch)
	result.Reasons = append(result.Reasons, fmt.Sprintf("use_case: %s", result.UseCase))

	// Step 3: Determine workflow from task type.
	result.Workflow = classifyWorkflow(result.TaskType)
	result.Reasons = append(result.Reasons, fmt.Sprintf("workflow: %s", result.Workflow))

	// Step 4: Select agent profile from task type.
	result.AgentProfile = classifyAgentProfile(result.TaskType)
	result.Reasons = append(result.Reasons, fmt.Sprintf("agent_profile: %s", result.AgentProfile))

	// Step 5: Select model route from task type + classification.
	result.ModelRoute = classifyModelRoute(result.TaskType, req.Classification)
	result.Reasons = append(result.Reasons, fmt.Sprintf("model_route: %s", result.ModelRoute))

	// Step 6: Determine evidence requirements from task type.
	result.EvidenceRequirements = classifyEvidenceRequirements(result.TaskType)
	result.Reasons = append(result.Reasons, fmt.Sprintf("evidence: %v", result.EvidenceRequirements))

	// Step 7: Determine risk, cost and latency posture.
	result.RiskLevel = classifyRiskLevel(text, branch)
	result.CostPosture = classifyCostPosture(result.TaskType)
	result.LatencyPosture = classifyLatencyPosture(result.TaskType)
	result.Reasons = append(result.Reasons, fmt.Sprintf("risk: %s, cost: %s, latency: %s", result.RiskLevel, result.CostPosture, result.LatencyPosture))

	// Step 8: Optional LLM suggestion (recorded but not authoritative).
	if c.llm != nil {
		suggestion, err := c.llm.Suggest(ctx, req)
		if err == nil && suggestion.Confidence > 0.5 {
			result.Reasons = append(result.Reasons,
				fmt.Sprintf("llm_suggestion: task=%s agent=%s confidence=%.2f reason=%s",
					suggestion.TaskType, suggestion.AgentProfile, suggestion.Confidence, suggestion.Reason))
			// If LLM disagrees strongly, note it but do not override.
			if suggestion.AgentProfile != result.AgentProfile && suggestion.Confidence > 0.8 {
				result.Reasons = append(result.Reasons,
					fmt.Sprintf("llm_disagreement: deterministic=%s llm=%s", result.AgentProfile, suggestion.AgentProfile))
			}
		}
	}

	return result, nil
}

func classifyTaskType(text string) string {
	// More specific checks first to avoid overlap.
	switch {
	case containsAny(text, "security", "secret", "auth", "vulnerability", "dependency risk", "data exposure"):
		return "security_scan"
	case containsAny(text, "test", "tests", "testing", "playwright", "unit test", "integration test", "coverage", "regression"):
		return "test_generation"
	case containsAny(text, "terraform", "infrastructure", "iac", "cloudformation"):
		return "infrastructure_review"
	case containsAny(text, "architecture", "design", "boundary", "data flow", "service", "deployment shape"):
		return "architecture_review"
	case containsAny(text, "frontend", "ui", "react", "component", "css", "html", "vue", "svelte", "angular"):
		return "frontend_development"
	case containsAny(text, "backend", "api", "server", "database", "microservice"):
		return "backend_development"
	case containsAny(text, "refactor", "cleanup", "clean up", "reorganize", "simplify", "rename"):
		return "refactor"
	case containsAny(text, "document", "readme", "docs", "documentation", "api explanation"):
		return "documentation"
	case containsAny(text, "review", "audit", "inspect", "validate", "verify"):
		return "review"
	case containsAny(text, "implement", "build", "create", "develop", "write code", "add feature"):
		return "implementation"
	default:
		return "general"
	}
}

func classifyUseCase(taskType, branch string) string {
	switch taskType {
	case "test_generation":
		return "quality_assurance"
	case "review":
		return "code_quality"
	case "refactor":
		return "maintenance"
	case "documentation":
		return "knowledge_management"
	case "security_scan":
		return "security_compliance"
	case "architecture_review":
		return "design_governance"
	case "infrastructure_review":
		return "platform_governance"
	case "implementation", "backend_development", "frontend_development":
		return "feature_delivery"
	default:
		return "general"
	}
}

func classifyWorkflow(taskType string) string {
	switch taskType {
	case "implementation", "backend_development", "frontend_development":
		return "plan_execute_review"
	case "test_generation":
		return "plan_execute_verify"
	case "review", "security_scan", "architecture_review", "infrastructure_review":
		return "investigate_report"
	case "refactor":
		return "plan_execute_verify"
	case "documentation":
		return "draft_review_publish"
	default:
		return "investigate_plan"
	}
}

func classifyAgentProfile(taskType string) string {
	switch taskType {
	case "test_generation":
		return "unit-tests"
	case "review":
		return "code-review"
	case "refactor":
		return "refactor"
	case "documentation":
		return "documentation"
	case "security_scan":
		return "security-scan"
	case "architecture_review":
		return "architecture-review"
	case "infrastructure_review":
		return "terraform-review"
	case "implementation":
		return "backend-development"
	case "backend_development":
		return "backend-development"
	case "frontend_development":
		return "frontend-development"
	default:
		return "code-review"
	}
}

func classifyModelRoute(taskType, classification string) string {
	// High-sensitivity work gets stronger models.
	switch taskType {
	case "security_scan", "architecture_review", "infrastructure_review":
		return "coding-primary"
	case "test_generation", "review":
		if classification == "restricted" || classification == "confidential" {
			return "coding-primary"
		}
		return "coding-balanced"
	case "implementation", "backend_development", "frontend_development":
		return "coding-balanced"
	case "refactor", "documentation":
		return "coding-fast"
	default:
		return "coding-balanced"
	}
}

func classifyEvidenceRequirements(taskType string) []string {
	switch taskType {
	case "test_generation":
		return []string{"test_results", "coverage_report"}
	case "review":
		return []string{"review_findings", "risk_assessment"}
	case "security_scan":
		return []string{"security_findings", "remediation_plan"}
	case "architecture_review":
		return []string{"design_review", "tradeoff_analysis"}
	case "infrastructure_review":
		return []string{"cost_analysis", "compliance_check"}
	case "implementation", "backend_development", "frontend_development":
		return []string{"implementation_notes", "test_results"}
	default:
		return []string{"summary"}
	}
}

func classifyRiskLevel(text, branch string) string {
	if containsAny(text, "security", "secret", "auth", "production", "prod", "live", "customer data") {
		return "high"
	}
	if containsAny(branch, "hotfix", "security", "prod") {
		return "high"
	}
	if containsAny(text, "refactor", "cleanup", "documentation", "readme") {
		return "low"
	}
	return "medium"
}

func classifyCostPosture(taskType string) string {
	switch taskType {
	case "refactor", "documentation":
		return "economy"
	case "security_scan", "architecture_review":
		return "performance"
	default:
		return "controlled"
	}
}

func classifyLatencyPosture(taskType string) string {
	switch taskType {
	case "refactor", "documentation":
		return "fast"
	case "security_scan", "architecture_review":
		return "thorough"
	default:
		return "balanced"
	}
}

func containsAny(text string, substrs ...string) bool {
	for _, s := range substrs {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}
