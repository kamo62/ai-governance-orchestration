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
	SessionID      string
	UserID         string
	AgentName      string
	ActionType     string
	Resource       string
	ToolName       string
	Classification string
	Findings       []string
	Metadata       map[string]any
}

type Decision struct {
	Allowed          bool
	RequiresApproval bool
	Reason           string
	Engine           string
	DecisionID       string
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
	if len(req.Findings) > 0 {
		decision.Allowed = false
		decision.Reason = "policy findings present"
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
