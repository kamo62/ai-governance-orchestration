package policyengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type Engine interface {
	Name() string
	Evaluate(context.Context, Request) (Decision, error)
}

type Request struct {
	SessionID        string
	UserID           string
	AgentName        string
	ActionType       string
	Resource         string
	ToolName         string
	Classification   string
	Prompt           string
	EstimatedCostUSD float64
	UseCaseID        string
	WorkflowID       string
	WorkItemID       string
	RiskLevel        string
	Findings         []string
	Metadata         map[string]any
}

type Decision struct {
	Allowed          bool
	RequiresApproval bool
	Reason           string
	Category         string
	Engine           string
	DecisionID       string
	Findings         []string
	CostCapUSD       float64
	RequiredEvidence []string
}

func New(name string) (Engine, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "native":
		return NewNativeEngine(NativeConfig{})
	case "agt":
		return nil, errors.New("policy engine agt is not implemented")
	default:
		return nil, fmt.Errorf("unknown policy engine %q", name)
	}
}

type NativeConfig struct {
	PolicyDir         string
	ClassificationMax string
	CostCapEnabled    bool
	SessionCostCapUSD float64
}

type NativeEngine struct {
	policies          policyBundle
	classificationMax string
	costCapEnabled    bool
	sessionCostCapUSD float64
}

type policyBundle struct {
	classifications map[string]int
	secretPatterns  []compiledSecretPattern
	costDefaults    costDefaults
	sdlcWorkflows   map[string]sdlcWorkflowRule
}

type compiledSecretPattern struct {
	name     string
	severity string
	re       *regexp.Regexp
}

type classificationPolicyFile struct {
	Classifications []classificationRule `yaml:"classifications"`
}

type classificationRule struct {
	Name string `yaml:"name"`
	Rank int    `yaml:"rank"`
}

type secretsPolicyFile struct {
	Patterns []secretRule `yaml:"patterns"`
}

type secretRule struct {
	Name     string `yaml:"name"`
	Regex    string `yaml:"regex"`
	Severity string `yaml:"severity"`
}

type costPolicyFile struct {
	Defaults costDefaults `yaml:"defaults"`
}

type costDefaults struct {
	RouterAgentCapUSD      float64 `yaml:"router_agent_cap_usd"`
	SpecialistCapUSD       float64 `yaml:"specialist_cap_usd"`
	SelfRepairCapUSD       float64 `yaml:"self_repair_cap_usd"`
	MaxRepairAttempts      int     `yaml:"max_repair_attempts"`
	MaxRuntimeMinutes      int     `yaml:"max_runtime_minutes"`
	ConsecutiveToolCallMax int     `yaml:"consecutive_tool_call_max"`
}

type sdlcPolicyFile struct {
	Workflows []sdlcWorkflowRule `yaml:"workflows"`
}

type sdlcWorkflowRule struct {
	ID                   string   `yaml:"id"`
	RequiresApproval     bool     `yaml:"requires_approval"`
	RequiresWorkItem     bool     `yaml:"requires_work_item"`
	RequiredEvidence     []string `yaml:"required_evidence"`
	MinimumEvidenceCount int      `yaml:"minimum_evidence_count"`
}

func NewNativeEngine(cfg NativeConfig) (*NativeEngine, error) {
	policies := defaultPolicyBundle()
	if cfg.PolicyDir != "" {
		loaded, err := loadPolicyBundle(cfg.PolicyDir, policies)
		if err != nil {
			return nil, err
		}
		policies = loaded
	}

	return &NativeEngine{
		policies:          policies,
		classificationMax: defaultString(cfg.ClassificationMax, "internal"),
		costCapEnabled:    cfg.CostCapEnabled,
		sessionCostCapUSD: cfg.SessionCostCapUSD,
	}, nil
}

func (NativeEngine) Name() string {
	return "native"
}

func (e *NativeEngine) Evaluate(_ context.Context, req Request) (Decision, error) {
	decision := Decision{
		Allowed:    true,
		Reason:     "allowed",
		Engine:     "native",
		DecisionID: newDecisionID(),
	}
	if len(req.Findings) > 0 {
		decision.Allowed = false
		decision.Category = "secret"
		decision.Reason = "policy findings present"
		decision.Findings = append([]string(nil), req.Findings...)
		return decision, nil
	}
	if req.Classification != "" {
		exceeds, err := e.classificationExceedsMax(req.Classification)
		if err != nil {
			return Decision{}, err
		}
		if exceeds {
			decision.Allowed = false
			decision.Category = "classification"
			decision.Reason = fmt.Sprintf("classification %s exceeds max %s", normalize(req.Classification), e.normalizedClassificationMax())
			return decision, nil
		}
	}
	if req.Prompt != "" {
		findings := e.detectSecrets(req.Prompt)
		if len(findings) > 0 {
			decision.Allowed = false
			decision.Category = "secret"
			decision.Reason = "secret detected"
			decision.Findings = findings
			return decision, nil
		}
	}
	if e.costCapEnabled && req.EstimatedCostUSD > 0 {
		capUSD := e.activeSessionCostCap()
		if capUSD > 0 && req.EstimatedCostUSD > capUSD {
			decision.Allowed = false
			decision.Category = "cost"
			decision.Reason = "cost cap exceeded"
			decision.CostCapUSD = capUSD
			return decision, nil
		}
	}
	if req.WorkflowID != "" {
		if rule, ok := e.policies.sdlcWorkflows[normalize(req.WorkflowID)]; ok {
			if rule.RequiresWorkItem && strings.TrimSpace(req.WorkItemID) == "" {
				decision.Allowed = false
				decision.Category = "sdlc"
				decision.Reason = fmt.Sprintf("workflow %s requires a work item", req.WorkflowID)
				decision.RequiredEvidence = append([]string(nil), rule.RequiredEvidence...)
				return decision, nil
			}
			decision.RequiresApproval = rule.RequiresApproval
			decision.RequiredEvidence = append([]string(nil), rule.RequiredEvidence...)
			if rule.RequiresApproval {
				decision.Category = "sdlc"
				decision.Reason = "sdlc workflow requires approval"
			}
		}
	}
	return decision, nil
}

func (e *NativeEngine) classificationExceedsMax(classification string) (bool, error) {
	classification = normalize(classification)
	max := e.normalizedClassificationMax()
	classificationValue, ok := e.policies.classifications[classification]
	if !ok {
		return false, fmt.Errorf("unknown classification %q", classification)
	}
	maxValue, ok := e.policies.classifications[max]
	if !ok {
		return false, fmt.Errorf("unknown max classification %q", max)
	}
	return classificationValue > maxValue, nil
}

func (e *NativeEngine) normalizedClassificationMax() string {
	max := normalize(e.classificationMax)
	if max == "" {
		return "internal"
	}
	return max
}

func (e *NativeEngine) detectSecrets(text string) []string {
	seen := map[string]struct{}{}
	var findings []string
	for _, pattern := range e.policies.secretPatterns {
		if pattern.re.MatchString(text) {
			if _, ok := seen[pattern.name]; ok {
				continue
			}
			seen[pattern.name] = struct{}{}
			findings = append(findings, pattern.name)
		}
	}
	return findings
}

func (e *NativeEngine) SecretFindings(text string) []string {
	if e == nil {
		return DefaultSecretFindings(text)
	}
	return e.detectSecrets(text)
}

func DefaultSecretFindings(text string) []string {
	engine, err := NewNativeEngine(NativeConfig{})
	if err != nil {
		return nil
	}
	return engine.detectSecrets(text)
}

func (e *NativeEngine) activeSessionCostCap() float64 {
	if e.sessionCostCapUSD > 0 {
		return e.sessionCostCapUSD
	}
	return e.policies.costDefaults.SpecialistCapUSD
}

func (e *NativeEngine) ActiveSessionCostCap() float64 {
	if e == nil || !e.costCapEnabled {
		return 0
	}
	return e.activeSessionCostCap()
}

func defaultPolicyBundle() policyBundle {
	patterns := []secretRule{
		{Name: "openrouter_api_key", Regex: `(?i)\b(?:OPENROUTER_API_KEY\s*=\s*)?sk-or-v1-[A-Za-z0-9_-]{10,}`, Severity: "critical"},
		{Name: "openai_api_key", Regex: `\bsk-[A-Za-z0-9]{20,}`, Severity: "critical"},
		{Name: "aws_access_key_id", Regex: `\bAKIA[0-9A-Z]{16}\b`, Severity: "critical"},
		{Name: "private_key", Regex: `-----BEGIN [A-Z ]*PRIVATE KEY-----`, Severity: "critical"},
		{Name: "password_assignment", Regex: `(?i)\b(password|passwd|pwd)\s*[:=]\s*[^[:space:]]{8,}`, Severity: "high"},
	}
	compiled, _ := compileSecretPatterns(patterns)
	return policyBundle{
		classifications: map[string]int{
			"public":       0,
			"internal":     1,
			"confidential": 2,
			"restricted":   3,
		},
		secretPatterns: compiled,
		costDefaults: costDefaults{
			RouterAgentCapUSD:      0.02,
			SpecialistCapUSD:       0.50,
			SelfRepairCapUSD:       0.20,
			MaxRepairAttempts:      2,
			MaxRuntimeMinutes:      15,
			ConsecutiveToolCallMax: 15,
		},
		sdlcWorkflows: map[string]sdlcWorkflowRule{},
	}
}

func loadPolicyBundle(dir string, base policyBundle) (policyBundle, error) {
	out := base
	if err := loadYAMLIfExists(filepath.Join(dir, "classification-routing.yaml"), func(data []byte) error {
		var file classificationPolicyFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			return err
		}
		if len(file.Classifications) == 0 {
			return errors.New("classification-routing.yaml must define classifications")
		}
		classifications := make(map[string]int, len(file.Classifications))
		for _, item := range file.Classifications {
			name := normalize(item.Name)
			if name == "" {
				return errors.New("classification name is required")
			}
			classifications[name] = item.Rank
		}
		out.classifications = classifications
		return nil
	}); err != nil {
		return policyBundle{}, err
	}
	if err := loadYAMLIfExists(filepath.Join(dir, "secrets-patterns.yaml"), func(data []byte) error {
		var file secretsPolicyFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			return err
		}
		compiled, err := compileSecretPatterns(file.Patterns)
		if err != nil {
			return err
		}
		out.secretPatterns = compiled
		return nil
	}); err != nil {
		return policyBundle{}, err
	}
	if err := loadYAMLIfExists(filepath.Join(dir, "cost-caps.yaml"), func(data []byte) error {
		var file costPolicyFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			return err
		}
		out.costDefaults = mergeCostDefaults(out.costDefaults, file.Defaults)
		return nil
	}); err != nil {
		return policyBundle{}, err
	}
	if err := loadYAMLIfExists(filepath.Join(dir, "sdlc-governance.yaml"), func(data []byte) error {
		var file sdlcPolicyFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			return err
		}
		workflows := make(map[string]sdlcWorkflowRule, len(file.Workflows))
		for _, workflow := range file.Workflows {
			id := normalize(workflow.ID)
			if id == "" {
				return errors.New("sdlc workflow id is required")
			}
			workflow.ID = id
			workflows[id] = workflow
		}
		out.sdlcWorkflows = workflows
		return nil
	}); err != nil {
		return policyBundle{}, err
	}
	return out, nil
}

func loadYAMLIfExists(path string, load func([]byte) error) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := load(data); err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	return nil
}

func compileSecretPatterns(patterns []secretRule) ([]compiledSecretPattern, error) {
	if len(patterns) == 0 {
		return nil, errors.New("secrets-patterns.yaml must define patterns")
	}
	out := make([]compiledSecretPattern, 0, len(patterns))
	for _, pattern := range patterns {
		name := normalize(pattern.Name)
		if name == "" {
			return nil, errors.New("secret pattern name is required")
		}
		if pattern.Regex == "" {
			return nil, fmt.Errorf("secret pattern %s regex is required", name)
		}
		compiled, err := regexp.Compile(pattern.Regex)
		if err != nil {
			return nil, fmt.Errorf("compile secret pattern %s: %w", name, err)
		}
		out = append(out, compiledSecretPattern{
			name:     name,
			severity: strings.TrimSpace(pattern.Severity),
			re:       compiled,
		})
	}
	return out, nil
}

func mergeCostDefaults(base, override costDefaults) costDefaults {
	if override.RouterAgentCapUSD > 0 {
		base.RouterAgentCapUSD = override.RouterAgentCapUSD
	}
	if override.SpecialistCapUSD > 0 {
		base.SpecialistCapUSD = override.SpecialistCapUSD
	}
	if override.SelfRepairCapUSD > 0 {
		base.SelfRepairCapUSD = override.SelfRepairCapUSD
	}
	if override.MaxRepairAttempts > 0 {
		base.MaxRepairAttempts = override.MaxRepairAttempts
	}
	if override.MaxRuntimeMinutes > 0 {
		base.MaxRuntimeMinutes = override.MaxRuntimeMinutes
	}
	if override.ConsecutiveToolCallMax > 0 {
		base.ConsecutiveToolCallMax = override.ConsecutiveToolCallMax
	}
	return base
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func newDecisionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "pol_decision_fallback"
	}
	return "pol_" + hex.EncodeToString(b[:])
}
