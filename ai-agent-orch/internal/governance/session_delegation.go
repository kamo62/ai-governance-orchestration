package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
)

// DelegatedTaskSessionRequest is emitted by a governed model endpoint when a
// client-local runtime asks its Task tool to run a known specialist agent. It
// records lineage only; local file-tool transcript forwarding is a separate
// client-side integration.
type DelegatedTaskSessionRequest struct {
	ParentSessionID     string
	ActorSubject        string
	ParentAgent         string
	Agent               string
	Description         string
	Prompt              string
	ModelAlias          string
	RequestedModelAlias string
	Provider            string
	ToolCallID          string
	SourceSystem        string
}

type DelegatedTaskSessionResult struct {
	Record     SessionRecord
	AuditEvent audit.Event
}

// CreateDelegatedTaskSession creates a non-executable child session marker for
// an observed client-local Task delegation. The child is intentionally marked
// "delegated" instead of "running": ai-orch has observed and governed the
// handoff, but it has not captured the child agent's local read/edit tool log.
func (s *SessionService) CreateDelegatedTaskSession(ctx context.Context, req DelegatedTaskSessionRequest) (DelegatedTaskSessionResult, error) {
	if s == nil || s.sessions == nil || s.audit == nil {
		return DelegatedTaskSessionResult{}, errors.New("delegated task sessions unavailable")
	}
	parentSessionID := strings.TrimSpace(req.ParentSessionID)
	if parentSessionID == "" {
		return DelegatedTaskSessionResult{}, errors.New("parent session ID is required")
	}
	agent := strings.TrimSpace(req.Agent)
	if agent == "" {
		return DelegatedTaskSessionResult{}, errors.New("delegated agent is required")
	}
	parent, err := s.sessions.Get(ctx, parentSessionID)
	if err != nil {
		return DelegatedTaskSessionResult{}, err
	}
	classification := strings.TrimSpace(parent.Classification)
	if classification == "" {
		classification = "internal"
	}
	actor := strings.TrimSpace(req.ActorSubject)
	if actor == "" {
		actor = strings.TrimSpace(parent.ActorSubject)
	}
	if actor == "" || !validActorLabel(actor) {
		actor = "runtime"
	}
	description := strings.TrimSpace(req.Description)
	prompt := strings.TrimSpace(req.Prompt)
	promptMaterial := prompt
	if promptMaterial == "" {
		promptMaterial = description
	}
	if promptMaterial == "" {
		promptMaterial = parentSessionID + ":" + agent + ":" + strings.TrimSpace(req.ToolCallID)
	}
	promptHash := sha256.Sum256([]byte(promptMaterial))
	promptHashHex := hex.EncodeToString(promptHash[:])
	sourceSystem := defaultString(strings.TrimSpace(req.SourceSystem), "opencode-task")
	policyCtx := WithAuthInfo(ctx, AuthInfo{Subject: actor, Method: "gateway"})
	if blocked, reason := s.blockedByKillSwitch(agent); blocked {
		_ = s.appendDelegationDenied(policyCtx, parentSessionID, agent, classification, reason, promptHashHex, req, "")
		return DelegatedTaskSessionResult{}, errors.New(reason)
	}
	decision, err := s.evaluatePolicy(policyCtx, policyengine.Request{
		AgentName:         agent,
		ActionType:        "session.delegate",
		Classification:    classification,
		ClassificationMax: s.classificationMax,
		Metadata: map[string]any{
			"parent_session_id": parentSessionID,
			"parent_agent":      defaultString(strings.TrimSpace(req.ParentAgent), parent.Agent),
			"description":       description,
			"prompt":            prompt,
			"model_alias":       strings.TrimSpace(req.ModelAlias),
			"tool_call_id":      strings.TrimSpace(req.ToolCallID),
			"source_system":     sourceSystem,
		},
		CostCapEnabled:    s.costCapEnabled,
		SessionCostCapUSD: s.sessionCostCapUSD,
	})
	if err != nil {
		_ = s.appendDelegationDenied(policyCtx, parentSessionID, agent, classification, "policy engine unavailable", promptHashHex, req, "")
		return DelegatedTaskSessionResult{}, errors.New("policy engine unavailable")
	}
	if !decision.Allowed {
		reason := decision.Reason
		if reason == "" {
			reason = "policy denied"
		}
		if err := s.recordPolicyDecision(policyCtx, policyengine.Request{
			SessionID:         parentSessionID,
			AgentName:         agent,
			ActionType:        "session.delegate",
			Classification:    classification,
			ClassificationMax: s.classificationMax,
			CostCapEnabled:    s.costCapEnabled,
			SessionCostCapUSD: s.sessionCostCapUSD,
		}, decision, parentSessionID, ""); err != nil {
			return DelegatedTaskSessionResult{}, errors.New("policy decision write failed")
		}
		_ = s.appendDelegationDenied(policyCtx, parentSessionID, agent, classification, reason, promptHashHex, req, decision.DecisionID)
		s.recordPolicyDenial(reason)
		return DelegatedTaskSessionResult{}, errors.New(reason)
	}

	sessionID := s.newID("sess_task")
	if err := s.recordPolicyDecision(policyCtx, policyengine.Request{
		SessionID:         sessionID,
		AgentName:         agent,
		ActionType:        "session.delegate",
		Classification:    classification,
		ClassificationMax: s.classificationMax,
		CostCapEnabled:    s.costCapEnabled,
		SessionCostCapUSD: s.sessionCostCapUSD,
	}, decision, sessionID, ""); err != nil {
		return DelegatedTaskSessionResult{}, errors.New("policy decision write failed")
	}
	eventID := s.newID("evt")
	now := time.Now().UTC()
	intent := description
	if intent == "" {
		intent = "OpenCode task delegation to " + agent
	}
	event, err := s.audit.Append(ctx, audit.Event{
		EventID:             eventID,
		ParentSessionID:     parentSessionID,
		SessionID:           sessionID,
		EventType:           "session.delegated",
		Actor:               actor,
		Agent:               agent,
		Classification:      classification,
		Reason:              description,
		PromptSHA256:        promptHashHex,
		Provider:            strings.TrimSpace(req.Provider),
		ModelAlias:          strings.TrimSpace(req.ModelAlias),
		RequestedModelAlias: strings.TrimSpace(req.RequestedModelAlias),
		RunID:               parent.RunID,
		PermissionMode:      parent.PermissionMode,
		ApprovalMode:        parent.ApprovalMode,
		WorkspaceMode:       parent.WorkspaceMode,
		WorkItemID:          parent.WorkItemID,
		WorkItemType:        parent.WorkItemType,
		CommitSHA:           parent.CommitSHA,
		ActorHint:           parent.ActorHint,
		SourceSystem:        sourceSystem,
		RawPromptStored:     false,
		RawResponseStored:   false,
		CorrelationSubject:  "opencode-task",
		TrustLevel:          "gateway_enforced",
		EnforcementMode:     "gateway",
		PolicyDecisionID:    decision.DecisionID,
		RecordedAt:          now,
	})
	if err != nil {
		return DelegatedTaskSessionResult{}, err
	}
	record := SessionRecord{
		SessionID:       sessionID,
		ParentSessionID: parentSessionID,
		ActorSubject:    actor,
		Agent:           agent,
		Classification:  classification,
		PromptSHA256:    promptHashHex,
		Status:          "delegated",
		CreatedAt:       now,
		PermissionMode:  parent.PermissionMode,
		ApprovalMode:    parent.ApprovalMode,
		WorkspaceMode:   parent.WorkspaceMode,
		UseCaseID:       parent.UseCaseID,
		WorkflowID:      parent.WorkflowID,
		WorkItemID:      parent.WorkItemID,
		WorkItemType:    parent.WorkItemType,
		RepoURL:         parent.RepoURL,
		Branch:          parent.Branch,
		CommitSHA:       parent.CommitSHA,
		Intent:          intent,
		ActorHint:       parent.ActorHint,
		SourceSystem:    sourceSystem,
	}
	if err := s.sessions.Create(ctx, record); err != nil {
		return DelegatedTaskSessionResult{}, err
	}
	s.rememberEventID(sessionID, event.EventID)
	s.recordSessionCreated()
	return DelegatedTaskSessionResult{Record: record, AuditEvent: event}, nil
}

func (s *SessionService) appendDelegationDenied(ctx context.Context, parentSessionID, agent, classification, reason, promptHash string, req DelegatedTaskSessionRequest, policyDecisionID string) error {
	if s == nil || s.audit == nil {
		return nil
	}
	_, err := s.audit.Append(ctx, audit.Event{
		EventID:             s.newID("evt"),
		ParentSessionID:     parentSessionID,
		SessionID:           parentSessionID,
		EventType:           "session.delegation_denied",
		Actor:               actorFromContext(ctx),
		Agent:               agent,
		Classification:      classification,
		Reason:              reason,
		PolicyDecisionID:    policyDecisionID,
		PromptSHA256:        promptHash,
		Provider:            strings.TrimSpace(req.Provider),
		ModelAlias:          strings.TrimSpace(req.ModelAlias),
		RequestedModelAlias: strings.TrimSpace(req.RequestedModelAlias),
		RawPromptStored:     false,
		RawResponseStored:   false,
		CorrelationSubject:  "opencode-task",
		TrustLevel:          "gateway_enforced",
		EnforcementMode:     "gateway",
		SourceSystem:        defaultString(strings.TrimSpace(req.SourceSystem), "opencode-task"),
	})
	if err == nil {
		s.recordSessionDenied()
	}
	return err
}
