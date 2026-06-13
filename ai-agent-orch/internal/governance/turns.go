package governance

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
)

// TurnsHandler serves POST /v1/sessions/{id}/turns for same-session follow-up dispatch.
// Managed clients (notably the VS Code Bridge POC) use this to continue a governed session
// without creating a new session_id.
type TurnsHandler struct {
	service  *SessionService
	orch     OrchestratorClient
	events   *EventStore
	executor *SessionExecutor
	newID    func(prefix string) string
}

func NewTurnsHandler(service *SessionService, orch OrchestratorClient, events *EventStore) http.Handler {
	executor := NewSessionExecutor(service, orch, events)
	return &TurnsHandler{
		service:  service,
		orch:     orch,
		events:   events,
		executor: executor,
		newID:    randomID,
	}
}

func (h *TurnsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.service == nil || h.service.audit == nil || h.orch == nil || h.events == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "turn service unavailable"})
		return
	}
	authReq, ok := h.service.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/turns")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	var req struct {
		Prompt      string `json:"prompt"`
		Agent       string `json:"agent,omitempty"`
		AutoConfirm bool   `json:"auto_confirm,omitempty"`
		UseCaseID   string `json:"use_case_id,omitempty"`
		WorkflowID  string `json:"workflow_id,omitempty"`
	}
	if err := readJSON(w, r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}

	if findings := policyengine.DetectSecrets(req.Prompt); len(findings) > 0 {
		if err := h.service.appendDenied(r.Context(), "secret detected in follow-up turn", findings, ""); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "secret detected"})
		return
	}

	actor := actorFromContext(r.Context())
	record, err := h.service.sessions.Get(r.Context(), sessionID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
		return
	}
	if record.ActorSubject != actor {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
		return
	}
	if !isTurnEligibleStatus(record.Status) {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{
			"error":  "session is not ready for a follow-up turn",
			"status": record.Status,
		})
		return
	}

	agent := strings.TrimSpace(req.Agent)
	if agent != "" {
		if blocked, reason := h.service.blockedByKillSwitch(agent); blocked {
			httpx.WriteJSON(w, http.StatusLocked, map[string]any{"error": reason})
			return
		}
	}

	h.events.Reopen(sessionID)
	h.service.rememberPrompt(sessionID, req.Prompt)

	routeCtx := SessionContext{
		RepoURL:      record.RepoURL,
		Branch:       record.Branch,
		CommitSHA:    record.CommitSHA,
		WorkItemID:   record.WorkItemID,
		WorkItemType: record.WorkItemType,
		ActorHint:    record.ActorHint,
		SourceSystem: record.SourceSystem,
	}

	specialist := agent
	reason := "follow-up turn agent override"
	var routeDecision RouteDecision
	if specialist == "" {
		decision, err := h.orch.Route(r.Context(), sessionID, req.Prompt, routeCtx)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
			return
		}
		routeDecision = decision
		specialist = decision.Specialist
		reason = decision.Reason
	}
	if routeDecision.HumanConfirmationRequired {
		req.AutoConfirm = false
	}

	trust := h.service.trustMetadataFromRequest(r)
	eventID := h.newID("evt")
	_, err = h.service.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		ParentEventID:      h.service.parentEventID(sessionID),
		SessionID:          sessionID,
		EventType:          "session.turn.requested",
		Actor:              actor,
		Agent:              specialist,
		Reason:             reason,
		Findings:           routingAuditFindings(routeDecision),
		RunID:              record.RunID,
		PermissionMode:     record.PermissionMode,
		ApprovalMode:       record.ApprovalMode,
		WorkspaceMode:      record.WorkspaceMode,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         trust.TrustLevel,
		EnforcementMode:    trust.EnforcementMode,
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}
	h.service.rememberEventID(sessionID, eventID)

	if !req.AutoConfirm {
		if err := h.service.setRoutedAgent(r.Context(), sessionID, specialist); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "routed agent update failed"})
			return
		}
		if err := h.service.sessions.UpdateStatus(r.Context(), sessionID, "awaiting_confirmation"); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session status update failed"})
			return
		}
		response := map[string]any{
			"session_id": sessionID,
			"status":     "awaiting_confirmation",
			"specialist": specialist,
			"reason":     reason,
			"next_gate":  "confirm",
			"sse_url":    "/v1/sessions/" + sessionID + "/events",
			"turn":       true,
		}
		addRoutingResponseFields(response, routeDecision)
		httpx.WriteJSON(w, http.StatusOK, response)
		return
	}

	if err := h.orch.AcceptSession(r.Context(), sessionID, specialist); err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("orchestrator accept failed: %v", err)})
		return
	}

	confirmEventID := h.newID("evt")
	_, err = h.service.audit.Append(r.Context(), audit.Event{
		EventID:            confirmEventID,
		ParentEventID:      h.service.parentEventID(sessionID),
		SessionID:          sessionID,
		EventType:          "session.turn.confirmed",
		Actor:              actor,
		Agent:              specialist,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         trust.TrustLevel,
		EnforcementMode:    trust.EnforcementMode,
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}
	h.service.rememberEventID(sessionID, confirmEventID)

	h.executor.PublishStream(sessionID, fmt.Sprintf("Follow-up turn for %s...", specialist))
	h.service.forgetPrompt(sessionID)
	h.executor.RunAsync(sessionID, specialist, req.Prompt)

	response := map[string]any{
		"session_id": sessionID,
		"status":     "running",
		"specialist": specialist,
		"reason":     reason,
		"sse_url":    "/v1/sessions/" + sessionID + "/events",
		"turn":       true,
	}
	addRoutingResponseFields(response, routeDecision)
	httpx.WriteJSON(w, http.StatusAccepted, response)
}

func isTurnEligibleStatus(status string) bool {
	switch status {
	case "done", "failed", "patch_ready":
		return true
	default:
		return false
	}
}
