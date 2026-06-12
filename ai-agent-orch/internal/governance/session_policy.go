package governance

import (
	"context"
	"fmt"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
)

func (s *SessionService) recordPolicyDenial(reason string) {
	if s == nil {
		return
	}
	switch reason {
	case "secret detected":
		s.recordSecretBlocked()
	default:
		if strings.HasPrefix(reason, "classification ") || strings.HasPrefix(reason, "unknown classification") {
			s.recordClassificationBlocked()
		}
	}
}

func (s *SessionService) appendDeniedWithCost(ctx context.Context, reason string, classification string, estimatedCostUSD float64, costCapUSD float64) error {
	if s == nil || s.audit == nil {
		return nil
	}
	_, err := s.audit.Append(ctx, audit.Event{
		EventID:            s.newID("evt"),
		EventType:          "session.denied",
		Actor:              actorFromContext(ctx),
		Classification:     classification,
		Reason:             reason,
		EstimatedCostUSD:   estimatedCostUSD,
		CostCapUSD:         costCapUSD,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
	})
	if err == nil {
		s.recordSessionDenied()
	}
	return err
}

func (s *SessionService) appendDenied(ctx context.Context, reason string, findings []string, classification string) error {
	if s == nil || s.audit == nil {
		return nil
	}
	_, err := s.audit.Append(ctx, audit.Event{
		EventID:            s.newID("evt"),
		EventType:          "session.denied",
		Actor:              actorFromContext(ctx),
		Classification:     classification,
		Reason:             reason,
		Findings:           findings,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
	})
	if err == nil {
		s.recordSessionDenied()
	}
	return err
}

func (s *SessionService) evaluatePolicy(ctx context.Context, req policyengine.Request) (policyengine.Decision, error) {
	if s == nil || s.policyEngine == nil {
		return policyengine.Decision{Allowed: true, Reason: "allowed", Engine: "native"}, nil
	}
	if req.UserID == "" {
		req.UserID = actorFromContext(ctx)
	}
	return s.policyEngine.Evaluate(ctx, req)
}

func (s *SessionService) recordSessionCreated() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordSessionCreated()
	}
}

func (s *SessionService) recordSessionDenied() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordSessionDenied()
	}
}

func (s *SessionService) recordSecretBlocked() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordSecretBlocked()
	}
}

func (s *SessionService) recordClassificationBlocked() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordClassificationBlocked()
	}
}

func (s *SessionService) recordCostCapped() {
	if s != nil && s.metrics != nil {
		s.metrics.RecordCostCapped()
	}
}

func (s *SessionService) blockedByKillSwitch(agent string) (bool, string) {
	if s == nil {
		return false, ""
	}
	if s.killSwitch {
		return true, "kill switch enabled"
	}
	if s.killSwitchStore == nil {
		return false, ""
	}
	if s.killSwitchStore.IsBlocked("global", "all") || s.killSwitchStore.IsBlocked("global", "sessions") {
		return true, "kill switch enabled"
	}
	if agent != "" && s.killSwitchStore.IsBlocked("agent", agent) {
		return true, fmt.Sprintf("kill switch enabled for agent %s", agent)
	}
	return false, ""
}

func (s *SessionService) blockedByClientKillSwitch(client string) (bool, string) {
	if s == nil || s.killSwitchStore == nil {
		return false, ""
	}
	client = strings.TrimSpace(client)
	if client == "" {
		return false, ""
	}
	if s.killSwitchStore.IsBlocked("client", client) {
		return true, fmt.Sprintf("kill switch enabled for client %s", client)
	}
	return false, ""
}
