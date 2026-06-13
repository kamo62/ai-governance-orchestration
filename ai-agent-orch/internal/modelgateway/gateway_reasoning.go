package modelgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

func applyGovernedReasoning(body []byte, decision router.Decision, session SessionInfo) ([]byte, router.Decision, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, decision, fmt.Errorf("decode request for reasoning policy: %w", err)
	}
	requested, requestedPresent, err := extractReasoningEffort(obj)
	if err != nil {
		return nil, decision, err
	}
	applied, source := chooseReasoningEffort(requested, requestedPresent, decision, session)
	decision.ReasoningEffortRequested = requested
	decision.ReasoningEffortApplied = applied
	decision.ReasoningSource = source
	encoded, err := rewriteReasoningEffort(obj, decision.ReasoningSupportsEffort, applied)
	if err != nil {
		return nil, decision, err
	}
	return encoded, decision, nil
}

func extractReasoningEffort(obj map[string]json.RawMessage) (string, bool, error) {
	for _, key := range []string{"reasoningEffort", "reasoning_effort"} {
		if raw, ok := obj[key]; ok {
			effort, err := reasoningEffortFromRaw(raw)
			if err != nil {
				return "", true, fmt.Errorf("%s must be one of low, medium, high", key)
			}
			return effort, true, nil
		}
	}
	if raw, ok := obj["reasoning"]; ok {
		var reasoning map[string]json.RawMessage
		if err := json.Unmarshal(raw, &reasoning); err == nil {
			if effortRaw, ok := reasoning["effort"]; ok {
				effort, err := reasoningEffortFromRaw(effortRaw)
				if err != nil {
					return "", true, errors.New("reasoning.effort must be one of low, medium, high")
				}
				return effort, true, nil
			}
		}
	}
	return "", false, nil
}

func reasoningEffortFromRaw(raw json.RawMessage) (string, error) {
	var effort string
	if err := json.Unmarshal(raw, &effort); err != nil {
		return "", err
	}
	effort = normalizeReasoningEffort(effort)
	if effort == "" {
		return "", errors.New("invalid reasoning effort")
	}
	return effort, nil
}

func chooseReasoningEffort(requested string, requestedPresent bool, decision router.Decision, session SessionInfo) (string, string) {
	agentDefault, agentMax := agentReasoningPolicy(session.Agent)
	if !decision.ReasoningSupportsEffort {
		if requestedPresent || agentDefault != "" || decision.ReasoningDefaultEffort != "" {
			return "", "provider_default"
		}
		return "", ""
	}
	applied := ""
	source := ""
	if requestedPresent {
		applied = requested
		source = "client"
	} else if agentDefault != "" {
		applied = agentDefault
		source = "agent_default"
	} else if decision.ReasoningDefaultEffort != "" {
		applied = decision.ReasoningDefaultEffort
		source = "route_default"
	}
	maxEffort := stricterReasoningMax(decision.ReasoningMaxEffort, agentMax)
	if applied != "" && maxEffort != "" && reasoningRank(applied) > reasoningRank(maxEffort) {
		applied = maxEffort
		source = "policy_clamped"
	}
	return applied, source
}

func rewriteReasoningEffort(obj map[string]json.RawMessage, supportsEffort bool, applied string) ([]byte, error) {
	delete(obj, "reasoningEffort")
	delete(obj, "reasoning_effort")
	if !supportsEffort {
		delete(obj, "reasoning")
		return json.Marshal(obj)
	}
	if applied == "" {
		return json.Marshal(obj)
	}
	reasoning := map[string]json.RawMessage{}
	if raw, ok := obj["reasoning"]; ok {
		_ = json.Unmarshal(raw, &reasoning)
	}
	effortJSON, err := json.Marshal(applied)
	if err != nil {
		return nil, fmt.Errorf("encode reasoning effort: %w", err)
	}
	reasoning["effort"] = effortJSON
	reasoningJSON, err := json.Marshal(reasoning)
	if err != nil {
		return nil, fmt.Errorf("encode reasoning object: %w", err)
	}
	obj["reasoning"] = reasoningJSON
	return json.Marshal(obj)
}

func agentReasoningPolicy(agent string) (defaultEffort string, maxEffort string) {
	switch strings.TrimSpace(agent) {
	case "governance-lead":
		return "low", "medium"
	case "code-review", "unit-tests", "backend-development", "frontend-development", "documentation", "refactor":
		return "medium", "high"
	case "security-review", "security-scan", "architecture-review", "terraform-review":
		return "medium", "high"
	default:
		return "", ""
	}
}

func stricterReasoningMax(routeMax string, agentMax string) string {
	routeMax = normalizeReasoningEffort(routeMax)
	agentMax = normalizeReasoningEffort(agentMax)
	if routeMax == "" {
		return agentMax
	}
	if agentMax == "" {
		return routeMax
	}
	if reasoningRank(agentMax) < reasoningRank(routeMax) {
		return agentMax
	}
	return routeMax
}

func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return ""
	}
}

func reasoningRank(effort string) int {
	switch normalizeReasoningEffort(effort) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	default:
		return 0
	}
}
