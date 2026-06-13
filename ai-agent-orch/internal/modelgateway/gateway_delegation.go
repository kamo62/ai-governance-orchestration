package modelgateway

import (
	"context"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

func (g *Gateway) recordTaskDelegations(ctx context.Context, parentSessionID string, session SessionInfo, decision router.Decision, summary toolCallAuditSummary) {
	if g == nil || g.delegateTask == nil || strings.TrimSpace(parentSessionID) == "" || len(summary.TaskDelegations) == 0 {
		return
	}
	actor := strings.TrimSpace(session.ActorSubject)
	parentAgent := strings.TrimSpace(session.Agent)
	sourceSystem := strings.TrimSpace(session.SourceSystem)
	if sourceSystem == "" {
		sourceSystem = "model-gateway-task"
	} else if !strings.HasSuffix(sourceSystem, "-task") {
		sourceSystem += "-task"
	}
	for _, delegation := range summary.TaskDelegations {
		agent := strings.TrimSpace(delegation.Agent)
		if agent == "" {
			continue
		}
		_ = g.delegateTask(ctx, TaskDelegationRequest{
			ParentSessionID:     parentSessionID,
			ActorSubject:        actor,
			ParentAgent:         parentAgent,
			Agent:               agent,
			Description:         strings.TrimSpace(delegation.Description),
			Prompt:              strings.TrimSpace(delegation.Prompt),
			ModelAlias:          decision.SelectedAlias,
			RequestedModelAlias: decision.RequestedAlias,
			Provider:            decision.Provider,
			ToolCallID:          delegation.ToolCallID,
			SourceSystem:        sourceSystem,
		})
	}
}
