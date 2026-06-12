package modelgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

func (g *Gateway) auditModelCall(ctx context.Context, sessionID string, session SessionInfo, decision router.Decision, eventType string, reqBody, respBody []byte, usage map[string]any, errMsg string) {
	if g.audit == nil {
		return
	}
	var reqHash, respHash string
	if len(reqBody) > 0 {
		reqHash = sha256Hex(reqBody)
	}
	if len(respBody) > 0 {
		respHash = sha256Hex(respBody)
	}
	g.auditModelCallHashesWithUsage(ctx, sessionID, session, decision, eventType, reqHash, respHash, usage, errMsg)
}

func (g *Gateway) auditModelCallHashes(ctx context.Context, sessionID string, session SessionInfo, decision router.Decision, eventType string, reqHash, respHash, errMsg string) {
	g.auditModelCallHashesWithUsage(ctx, sessionID, session, decision, eventType, reqHash, respHash, nil, errMsg)
}

func (g *Gateway) auditModelCallHashesWithUsage(ctx context.Context, sessionID string, session SessionInfo, decision router.Decision, eventType string, reqHash, respHash string, usage map[string]any, errMsg string) {
	if g.audit == nil {
		return
	}
	reason := ""
	if errMsg != "" {
		reason = errMsg
	}
	actor := strings.TrimSpace(session.ActorSubject)
	if actor == "" {
		actor = "runtime"
	}
	_, _ = g.audit.Append(ctx, audit.Event{
		EventID:                  g.newID("evt"),
		SessionID:                sessionID,
		EventType:                eventType,
		Actor:                    actor,
		Agent:                    session.Agent,
		Classification:           session.Classification,
		Provider:                 decision.Provider,
		ModelAlias:               decision.SelectedAlias,
		ModelResolved:            g.resolvedModel(decision),
		RequestedModelAlias:      decision.RequestedAlias,
		CredentialSource:         decision.CredentialSource,
		ReasoningEffortRequested: decision.ReasoningEffortRequested,
		ReasoningEffortApplied:   decision.ReasoningEffortApplied,
		ReasoningSource:          decision.ReasoningSource,
		RequestSHA256:            reqHash,
		ResponseSHA256:           respHash,
		TokenUsage:               usage,
		GatewayBackend:           g.backendName(),
		RunID:                    session.RunID,
		PermissionMode:           session.PermissionMode,
		ApprovalMode:             session.ApprovalMode,
		WorkspaceMode:            session.WorkspaceMode,
		WorkItemID:               session.WorkItemID,
		WorkItemType:             session.WorkItemType,
		CommitSHA:                session.CommitSHA,
		ActorHint:                session.ActorHint,
		SourceSystem:             session.SourceSystem,
		TrustLevel:               "gateway_enforced",
		EnforcementMode:          "gateway",
		Reason:                   reason,
		RawPromptStored:          false,
		RawResponseStored:        false,
		CorrelationSubject:       "model-gateway",
	})
}

func openrouterUsageMap(usage *openrouter.Usage) map[string]any {
	if usage == nil {
		return nil
	}
	return map[string]any{
		"prompt_tokens":     usage.PromptTokens,
		"completion_tokens": usage.CompletionTokens,
		"total_tokens":      usage.TotalTokens,
		"reasoning_tokens":  usage.CompletionTokensDetails.ReasoningTokens,
		"cost_usd":          usage.Cost,
	}
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
