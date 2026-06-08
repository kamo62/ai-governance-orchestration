package governance

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/policyengine"
)

// OrchestratorClient is the interface used by the Governance Shell to call the Orchestrator.
type OrchestratorClient interface {
	Route(ctx context.Context, sessionID string, prompt string, context SessionContext) (RouteDecision, error)
	AcceptSession(ctx context.Context, sessionID string, agent string) error
	Dispatch(ctx context.Context, sessionID string, agent string, prompt string) (DispatchResult, error)
}

// RouteDecision is the response from the Orchestrator router.
type RouteDecision struct {
	Specialist string `json:"specialist"`
	Reason     string `json:"reason"`
}

// DispatchResult is the response from the Orchestrator dispatch endpoint.
type DispatchResult struct {
	SessionID string          `json:"session_id"`
	Status    string          `json:"status"`
	Agent     string          `json:"agent"`
	Events    []DispatchEvent `json:"events"`
}

// DispatchEvent represents a single runtime event.
type DispatchEvent struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

// MessagesHandler serves POST /v1/sessions/{id}/messages.
type MessagesHandler struct {
	service *SessionService
	orch    OrchestratorClient
	newID   func(prefix string) string
}

func NewMessagesHandler(service *SessionService, orch OrchestratorClient) http.Handler {
	return &MessagesHandler{
		service: service,
		orch:    orch,
		newID:   randomID,
	}
}

func (h *MessagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.service == nil || h.service.audit == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session service unavailable"})
		return
	}
	authReq, ok := h.service.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	sessionID := extractSessionID(r.URL.Path, "/v1/sessions/", "/messages")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}
	if blocked, reason := h.service.blockedByKillSwitch(""); blocked {
		if err := h.service.appendDenied(r.Context(), reason, nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		writeJSON(w, http.StatusLocked, map[string]any{"error": reason})
		return
	}

	// Enforce session ownership.
	actor := actorFromContext(r.Context())
	if h.service.sessions != nil {
		record, err := h.service.sessions.Get(r.Context(), sessionID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
			return
		}
		if record.ActorSubject != actor {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
			return
		}
		if record.Status != "created" {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "session is not awaiting routing"})
			return
		}
	}

	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}

	// Re-check secrets on the new prompt.
	if findings := policyengine.DetectSecrets(req.Prompt); len(findings) > 0 {
		if err := h.service.appendDenied(r.Context(), "secret detected in follow-up message", findings, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "secret detected"})
		return
	}

	// Load session context for routing enrichment.
	var routeCtx SessionContext
	var record SessionRecord
	if h.service.sessions != nil {
		if rec, err := h.service.sessions.Get(r.Context(), sessionID); err == nil {
			record = rec
			routeCtx = SessionContext{
				RepoURL:      rec.RepoURL,
				Branch:       rec.Branch,
				CommitSHA:    rec.CommitSHA,
				WorkItemID:   rec.WorkItemID,
				WorkItemType: rec.WorkItemType,
				ActorHint:    rec.ActorHint,
				SourceSystem: rec.SourceSystem,
			}
		}
	}

	// Call Orchestrator for routing.
	decision, err := h.orch.Route(r.Context(), sessionID, req.Prompt, routeCtx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}

	// Transition session to awaiting_confirmation after successful routing.
	if h.service.sessions != nil {
		if err := h.service.sessions.UpdateStatus(r.Context(), sessionID, "awaiting_confirmation"); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "session status update failed"})
			return
		}
	}

	// Record router-selection audit event, linked to session creation.
	eventID := h.newID("evt")
	trust := h.service.trustMetadataFromRequest(r)
	_, err = h.service.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		ParentEventID:      h.service.parentEventID(sessionID),
		SessionID:          sessionID,
		EventType:          "router.specialist.selected",
		Actor:              actor,
		Agent:              decision.Specialist,
		Reason:             decision.Reason,
		RunID:              record.RunID,
		PermissionMode:     record.PermissionMode,
		ApprovalMode:       record.ApprovalMode,
		WorkspaceMode:      record.WorkspaceMode,
		WorkItemID:         record.WorkItemID,
		WorkItemType:       record.WorkItemType,
		CommitSHA:          record.CommitSHA,
		ActorHint:          record.ActorHint,
		SourceSystem:       record.SourceSystem,
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "governance-shell",
		TrustLevel:         trust.TrustLevel,
		EnforcementMode:    trust.EnforcementMode,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}
	h.service.rememberEventID(sessionID, eventID)
	h.service.rememberPrompt(sessionID, req.Prompt)

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":     sessionID,
		"status":         "awaiting_confirmation",
		"specialist":     decision.Specialist,
		"reason":         decision.Reason,
		"audit_event_id": eventID,
	})
}

func extractSessionID(path string, prefix string, suffix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	trimmed := strings.TrimPrefix(path, prefix)
	if !strings.HasSuffix(trimmed, suffix) {
		return ""
	}
	return strings.TrimSuffix(trimmed, suffix)
}
