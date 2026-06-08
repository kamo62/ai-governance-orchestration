package policyengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

type Engine interface {
	Name() string
	Evaluate(context.Context, Request) (Decision, error)
}

type Request struct {
	SessionID         string
	UserID            string
	AgentName         string
	ActionType        string
	Resource          string
	ToolName          string
	Classification    string
	ClassificationMax string
	Findings          []string
	Metadata          map[string]any
	// Cost controls
	CostCapEnabled    bool
	SessionCostCapUSD float64
	EstimatedCostUSD  float64
}

type Decision struct {
	Allowed          bool
	RequiresApproval bool
	Reason           string
	Engine           string
	DecisionID       string
	Findings         []string
}

func New(name string) (Engine, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "native":
		return NativeEngine{}, nil
	case "agt":
		return nil, errors.New("policy engine agt is not implemented")
	default:
		return nil, fmt.Errorf("unknown policy engine %q", name)
	}
}

type NativeEngine struct{}

func (NativeEngine) Name() string {
	return "native"
}

func (NativeEngine) Evaluate(_ context.Context, req Request) (Decision, error) {
	decision := Decision{
		Allowed:    true,
		Reason:     "allowed",
		Engine:     "native",
		DecisionID: newDecisionID(),
	}

	// 1. Classification ceiling
	exceeds, err := classificationExceedsMax(req.Classification, req.ClassificationMax)
	if err != nil {
		return Decision{Allowed: false, Reason: err.Error(), Engine: "native", DecisionID: decision.DecisionID}, nil
	}
	if exceeds {
		max := strings.TrimSpace(req.ClassificationMax)
		if max == "" {
			max = "internal"
		}
		return Decision{Allowed: false, Reason: fmt.Sprintf("classification %s exceeds max %s", req.Classification, max), Engine: "native", DecisionID: decision.DecisionID}, nil
	}

	if req.ActionType == "mcp.tool_call" {
		return evaluateMCPToolCall(req, decision), nil
	}

	// 2. Secret detection
	findings := req.Findings
	if len(findings) == 0 && req.Metadata != nil {
		if prompt, ok := req.Metadata["prompt"].(string); ok && prompt != "" {
			findings = DetectSecrets(prompt)
		}
	}
	if len(findings) > 0 {
		return Decision{Allowed: false, Reason: "secret detected", Engine: "native", DecisionID: decision.DecisionID, Findings: findings}, nil
	}

	// 3. Cost cap
	if req.CostCapEnabled && req.SessionCostCapUSD > 0 && req.EstimatedCostUSD > req.SessionCostCapUSD {
		return Decision{Allowed: false, Reason: "cost cap exceeded", Engine: "native", DecisionID: decision.DecisionID}, nil
	}

	return decision, nil
}

func evaluateMCPToolCall(req Request, decision Decision) Decision {
	allowedAgents := stringSliceMetadata(req.Metadata, "allowed_agents")
	toolAllow := stringSliceMetadata(req.Metadata, "tool_allow")
	toolDeny := stringSliceMetadata(req.Metadata, "tool_deny")

	if len(allowedAgents) > 0 && !stringSliceContains(allowedAgents, req.AgentName) {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("agent %q is not allowed to use mcp server %q", req.AgentName, req.Resource)
		return decision
	}
	if stringSliceContains(toolDeny, req.ToolName) {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("mcp tool %q is explicitly denied", req.ToolName)
		return decision
	}
	if len(toolAllow) == 0 || !stringSliceContains(toolAllow, req.ToolName) {
		decision.Allowed = false
		decision.Reason = fmt.Sprintf("mcp tool %q is not in allow list", req.ToolName)
		return decision
	}

	decision.Reason = "mcp tool allowed"
	return decision
}

func stringSliceMetadata(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	value, ok := metadata[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		var values []string
		for _, item := range typed {
			if s, ok := item.(string); ok {
				values = append(values, s)
			}
		}
		return values
	default:
		return nil
	}
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func newDecisionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "pol_decision_fallback"
	}
	return "pol_" + hex.EncodeToString(b[:])
}
