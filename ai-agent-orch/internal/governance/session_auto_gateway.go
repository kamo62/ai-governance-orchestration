package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
)

// sanitizeRepoURL strips any embedded credentials (user:pass@) from a remote URL
// before it is persisted or written to the audit ledger. scp-style and non-URL
// remotes pass through unchanged.
func sanitizeRepoURL(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

type AutoGatewaySessionRequest struct {
	ActorSubject          string
	Classification        string
	PromptSHA256          string
	ModelAlias            string
	Client                string
	Endpoint              string
	RawRequestBody        []byte
	TrustedClientToken    string
	UseCaseID             string
	WorkflowID            string
	WorkItemID            string
	WorkItemType          string
	RepoURL               string
	Branch                string
	CommitSHA             string
	Intent                string
	ActorHint             string
	SourceSystem          string
	EstimatedCostUSD      float64
	ClientSessionID       string
	ParentClientSessionID string
}

type AutoGatewaySessionResult struct {
	Record       SessionRecord
	GatewayToken string
	// Reused is true when an existing session was returned instead of creating
	// a new one (one governed session per client conversation).
	Reused bool
}

// sessionClientFinder is implemented by session stores that can resolve an
// existing active session for an actor's client conversation id, enabling
// auto-session reuse. Optional: stores without it always create new sessions.
type sessionClientFinder interface {
	FindActiveByActorClientSession(ctx context.Context, actorSubject, clientSessionID string) (SessionRecord, bool, error)
}

func (s *SessionService) CreateAutoGatewaySession(ctx context.Context, req AutoGatewaySessionRequest) (AutoGatewaySessionResult, error) {
	if s == nil || s.sessions == nil || s.audit == nil {
		return AutoGatewaySessionResult{}, errors.New("auto sessions unavailable")
	}
	actorSubject := strings.TrimSpace(req.ActorSubject)
	if actorSubject == "" || !validActorLabel(actorSubject) {
		return AutoGatewaySessionResult{}, errors.New("valid actor subject is required")
	}
	classification := strings.TrimSpace(req.Classification)
	if classification == "" {
		classification = "internal"
	}
	// Reuse one governed session per client conversation: when the caller sends
	// a stable client session id, an existing active session is returned instead
	// of minting a new one per model call. Git context is recorded once, at the
	// session that was originally created.
	if clientSessionID := strings.TrimSpace(req.ClientSessionID); clientSessionID != "" {
		if finder, ok := s.sessions.(sessionClientFinder); ok {
			if existing, found, lookupErr := finder.FindActiveByActorClientSession(ctx, actorSubject, clientSessionID); lookupErr == nil && found {
				return AutoGatewaySessionResult{Record: existing, Reused: true}, nil
			}
		}
	}
	parentSessionID := ""
	if parentClientSessionID := strings.TrimSpace(req.ParentClientSessionID); parentClientSessionID != "" && parentClientSessionID != strings.TrimSpace(req.ClientSessionID) {
		if finder, ok := s.sessions.(sessionClientFinder); ok {
			if parent, found, lookupErr := finder.FindActiveByActorClientSession(ctx, actorSubject, parentClientSessionID); lookupErr == nil && found {
				parentSessionID = parent.SessionID
			}
		}
	}
	request := CreateSessionRequest{
		Agent:            "model-gateway",
		Classification:   classification,
		Prompt:           string(req.RawRequestBody),
		EstimatedCostUSD: req.EstimatedCostUSD,
		PermissionMode:   "reviewed",
		ApprovalMode:     "self_reported",
		WorkspaceMode:    "client-local",
		UseCaseID:        strings.TrimSpace(req.UseCaseID),
		WorkflowID:       strings.TrimSpace(req.WorkflowID),
		WorkItemID:       strings.TrimSpace(req.WorkItemID),
		WorkItemType:     strings.TrimSpace(req.WorkItemType),
		RepoURL:          sanitizeRepoURL(req.RepoURL),
		Branch:           strings.TrimSpace(req.Branch),
		CommitSHA:        strings.TrimSpace(req.CommitSHA),
		Intent:           strings.TrimSpace(req.Intent),
		ActorHint:        strings.TrimSpace(req.ActorHint),
		SourceSystem:     defaultString(strings.TrimSpace(req.SourceSystem), defaultString(strings.TrimSpace(req.Client), "model-gateway")),
	}
	request.normalizeRuntimeModes()
	if strings.TrimSpace(request.Prompt) == "" {
		request.Prompt = strings.TrimSpace(req.PromptSHA256)
	}
	if blocked, reason := s.blockedByKillSwitch(""); blocked {
		_ = s.appendDenied(WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"}), reason, nil, classification)
		return AutoGatewaySessionResult{}, errors.New(reason)
	}
	if err := s.enforceWorkItemContext(&request); err != nil {
		_ = s.appendDenied(WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"}), err.Error(), nil, classification)
		return AutoGatewaySessionResult{}, err
	}
	if blocked, reason := s.blockedByKillSwitch(request.Agent); blocked {
		_ = s.appendDenied(WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"}), reason, nil, classification)
		return AutoGatewaySessionResult{}, errors.New(reason)
	}
	if blocked, reason := s.blockedByClientKillSwitch(strings.TrimSpace(req.Client)); blocked {
		_ = s.appendDenied(WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"}), reason, nil, classification)
		return AutoGatewaySessionResult{}, errors.New(reason)
	}
	policyCtx := WithAuthInfo(ctx, AuthInfo{Subject: actorSubject, Method: "self_reported"})
	decision, err := s.evaluatePolicy(policyCtx, policyengine.Request{
		AgentName:         request.Agent,
		ActionType:        "session.auto_create",
		Classification:    request.Classification,
		ClassificationMax: s.classificationMax,
		Metadata: map[string]any{
			"prompt_length": len(request.Prompt),
			"prompt":        request.Prompt,
			"model_alias":   strings.TrimSpace(req.ModelAlias),
			"client":        strings.TrimSpace(req.Client),
			"endpoint":      strings.TrimSpace(req.Endpoint),
		},
		CostCapEnabled:    s.costCapEnabled,
		SessionCostCapUSD: s.sessionCostCapUSD,
		EstimatedCostUSD:  request.EstimatedCostUSD,
	})
	if err != nil {
		_ = s.appendDenied(policyCtx, "policy engine unavailable", nil, classification)
		return AutoGatewaySessionResult{}, errors.New("policy engine unavailable")
	}
	policySessionID := ""
	policyCorrelationID := ""
	if decision.Allowed {
		policySessionID = s.newID("sess_auto")
	} else {
		policyCorrelationID = randomID("corr")
	}
	if err := s.recordPolicyDecision(policyCtx, policyengine.Request{
		AgentName:         request.Agent,
		ActionType:        "session.auto_create",
		Classification:    request.Classification,
		ClassificationMax: s.classificationMax,
		CostCapEnabled:    s.costCapEnabled,
		SessionCostCapUSD: s.sessionCostCapUSD,
		EstimatedCostUSD:  request.EstimatedCostUSD,
	}, decision, policySessionID, policyCorrelationID); err != nil {
		return AutoGatewaySessionResult{}, errors.New("policy decision write failed")
	}
	if !decision.Allowed {
		reason := decision.Reason
		if reason == "" {
			reason = "policy denied"
		}
		if reason == "cost cap exceeded" {
			_ = s.appendDeniedWithCost(policyCtx, reason, classification, request.EstimatedCostUSD, s.sessionCostCapUSD, policyAuditLink{decisionID: decision.DecisionID, correlationID: policyCorrelationID})
			s.recordCostCapped()
		} else {
			_ = s.appendDenied(policyCtx, reason, decision.Findings, classification, policyAuditLink{decisionID: decision.DecisionID, correlationID: policyCorrelationID})
			s.recordPolicyDenial(reason)
		}
		return AutoGatewaySessionResult{}, errors.New(reason)
	}
	promptHash := strings.TrimSpace(req.PromptSHA256)
	modelAlias := strings.TrimSpace(req.ModelAlias)
	endpoint := strings.TrimSpace(req.Endpoint)
	sessionID := policySessionID
	eventID := s.newID("evt")
	now := time.Now().UTC()
	trust := s.trustMetadataFromClient(strings.TrimSpace(req.Client), strings.TrimSpace(req.TrustedClientToken))
	gatewayToken := s.newID("sgt")
	tokenHash := sha256.Sum256([]byte(gatewayToken))
	event, err := s.audit.Append(ctx, audit.Event{
		EventID:            eventID,
		ParentSessionID:    parentSessionID,
		SessionID:          sessionID,
		EventType:          "session.auto_created",
		Actor:              actorSubject,
		Agent:              request.Agent,
		Classification:     classification,
		PermissionMode:     request.PermissionMode,
		ApprovalMode:       request.ApprovalMode,
		WorkspaceMode:      request.WorkspaceMode,
		WorkItemID:         request.WorkItemID,
		WorkItemType:       request.WorkItemType,
		RepoURL:            request.RepoURL,
		Branch:             request.Branch,
		CommitSHA:          request.CommitSHA,
		ActorHint:          request.ActorHint,
		SourceSystem:       request.SourceSystem,
		PromptSHA256:       promptHash,
		ModelAlias:         modelAlias,
		Reason:             "auto session for model gateway " + endpoint,
		EstimatedCostUSD:   request.EstimatedCostUSD,
		CostCapUSD:         activeCostCap(s.costCapEnabled, s.sessionCostCapUSD),
		PolicyDecisionID:   decision.DecisionID,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "model-gateway",
		TrustLevel:         trust.TrustLevel,
		EnforcementMode:    trust.EnforcementMode,
		RecordedAt:         now,
	})
	if err != nil {
		return AutoGatewaySessionResult{}, err
	}
	rec := SessionRecord{
		SessionID:          sessionID,
		ParentSessionID:    parentSessionID,
		GatewayTokenSHA256: hex.EncodeToString(tokenHash[:]),
		ActorSubject:       actorSubject,
		Agent:              request.Agent,
		Classification:     classification,
		PromptSHA256:       promptHash,
		Status:             "running",
		CreatedAt:          now,
		PermissionMode:     request.PermissionMode,
		ApprovalMode:       request.ApprovalMode,
		WorkspaceMode:      request.WorkspaceMode,
		UseCaseID:          request.UseCaseID,
		WorkflowID:         request.WorkflowID,
		WorkItemID:         request.WorkItemID,
		WorkItemType:       request.WorkItemType,
		RepoURL:            request.RepoURL,
		Branch:             request.Branch,
		CommitSHA:          request.CommitSHA,
		Intent:             request.Intent,
		ActorHint:          request.ActorHint,
		SourceSystem:       request.SourceSystem,
		ClientSessionID:    strings.TrimSpace(req.ClientSessionID),
	}
	if err := s.sessions.Create(ctx, rec); err != nil {
		return AutoGatewaySessionResult{}, err
	}
	s.rememberEventID(sessionID, event.EventID)
	s.recordSessionCreated()
	return AutoGatewaySessionResult{Record: rec, GatewayToken: gatewayToken}, nil
}
