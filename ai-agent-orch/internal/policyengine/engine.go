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

func newDecisionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "pol_decision_fallback"
	}
	return "pol_" + hex.EncodeToString(b[:])
}
