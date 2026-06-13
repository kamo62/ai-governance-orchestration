package governance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// RunResponse is returned by POST /v1/runs after the Shell creates a session
// and routes the first prompt to a specialist.
type RunResponse struct {
	RunID                     string   `json:"run_id"`
	SessionID                 string   `json:"session_id"`
	Status                    string   `json:"status"`
	Agent                     string   `json:"agent"`
	Specialist                string   `json:"specialist"`
	Reason                    string   `json:"reason,omitempty"`
	RoutingConfidence         string   `json:"routing_confidence,omitempty"`
	HumanConfirmationRequired bool     `json:"human_confirmation_required,omitempty"`
	RoutingAlternates         []string `json:"routing_alternates,omitempty"`
	NextGate                  string   `json:"next_gate,omitempty"`
	SSEURL                    string   `json:"sse_url"`
	AuditEventID              string   `json:"audit_event_id"`
	RouteAuditEventID         string   `json:"route_audit_event_id"`
	PermissionMode            string   `json:"permission_mode,omitempty"`
	ApprovalMode              string   `json:"approval_mode,omitempty"`
	WorkspaceMode             string   `json:"workspace_mode,omitempty"`
	// GatewayToken is the per-session model gateway secret, surfaced once.
	GatewayToken string `json:"gateway_token,omitempty"`
}

// RunHandler serves POST /v1/runs.
type RunHandler struct {
	service *SessionService
	orch    OrchestratorClient
}

func NewRunHandler(service *SessionService, orch OrchestratorClient) http.Handler {
	return &RunHandler{service: service, orch: orch}
}

func (h *RunHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if h.service == nil || h.service.audit == nil || h.orch == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "run service unavailable"})
		return
	}
	authReq, ok := h.service.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	var request CreateSessionRequest
	if err := readJSON(w, r, &request); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if request.RunID == "" {
		request.RunID = h.service.newID("run")
	}
	request.normalizeRuntimeModes()
	h.service.resolveSessionContext(&request)

	sessionBody, err := json.Marshal(request)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "encode run request failed"})
		return
	}
	sessionReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/v1/sessions", bytes.NewReader(sessionBody))
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session request failed"})
		return
	}
	sessionReq.Header = r.Header.Clone()
	sessionReq.Header.Set("Content-Type", "application/json")
	sessionRec := &captureResponseWriter{headers: http.Header{}}
	NewSessionHandler(h.service).ServeHTTP(sessionRec, sessionReq)
	if sessionRec.status != http.StatusCreated {
		copyCapturedResponse(w, sessionRec)
		return
	}

	var sessionResp CreateSessionResponse
	if err := json.Unmarshal(sessionRec.body.Bytes(), &sessionResp); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "parse session response failed"})
		return
	}

	routeCtx := SessionContext{
		RepoURL:      request.RepoURL,
		Branch:       request.Branch,
		CommitSHA:    request.CommitSHA,
		WorkItemID:   request.WorkItemID,
		WorkItemType: request.WorkItemType,
		ActorHint:    request.ActorHint,
		SourceSystem: request.SourceSystem,
	}
	if h.service.sessions != nil {
		if rec, err := h.service.sessions.Get(r.Context(), sessionResp.SessionID); err == nil {
			routeCtx = SessionContext{
				RepoURL:      rec.RepoURL,
				Branch:       rec.Branch,
				CommitSHA:    rec.CommitSHA,
				WorkItemID:   rec.WorkItemID,
				WorkItemType: rec.WorkItemType,
				ActorHint:    rec.ActorHint,
				SourceSystem: rec.SourceSystem,
			}
			request.PermissionMode = rec.PermissionMode
			request.ApprovalMode = rec.ApprovalMode
			request.WorkspaceMode = rec.WorkspaceMode
		}
	}

	decision, err := h.orch.Route(r.Context(), sessionResp.SessionID, request.Prompt, routeCtx)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("routing failed: %v", err)})
		return
	}
	if h.service.sessions != nil {
		if err := h.service.setRoutedAgent(r.Context(), sessionResp.SessionID, decision.Specialist); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "routed agent update failed"})
			return
		}
		if err := h.service.sessions.UpdateStatus(r.Context(), sessionResp.SessionID, "awaiting_confirmation"); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "session status update failed"})
			return
		}
	}

	eventID := h.service.newID("evt")
	trust := h.service.trustMetadataFromRequest(r)
	_, err = h.service.audit.Append(r.Context(), audit.Event{
		EventID:            eventID,
		ParentEventID:      h.service.parentEventID(sessionResp.SessionID),
		SessionID:          sessionResp.SessionID,
		EventType:          "router.specialist.selected",
		Actor:              actorFromContext(r.Context()),
		Agent:              decision.Specialist,
		Reason:             decision.Reason,
		Findings:           routingAuditFindings(decision),
		RunID:              request.RunID,
		PermissionMode:     request.PermissionMode,
		ApprovalMode:       request.ApprovalMode,
		WorkspaceMode:      request.WorkspaceMode,
		WorkItemID:         routeCtx.WorkItemID,
		WorkItemType:       routeCtx.WorkItemType,
		CommitSHA:          routeCtx.CommitSHA,
		ActorHint:          routeCtx.ActorHint,
		SourceSystem:       routeCtx.SourceSystem,
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
	h.service.rememberEventID(sessionResp.SessionID, eventID)
	h.service.rememberPrompt(sessionResp.SessionID, request.Prompt)

	httpx.WriteJSON(w, http.StatusCreated, RunResponse{
		RunID:                     request.RunID,
		SessionID:                 sessionResp.SessionID,
		Status:                    "awaiting_confirmation",
		Agent:                     sessionResp.Agent,
		Specialist:                decision.Specialist,
		Reason:                    decision.Reason,
		RoutingConfidence:         decision.RoutingConfidence,
		HumanConfirmationRequired: decision.HumanConfirmationRequired,
		RoutingAlternates:         decision.RoutingAlternates,
		NextGate:                  "confirm",
		SSEURL:                    "/v1/sessions/" + sessionResp.SessionID + "/events",
		AuditEventID:              sessionResp.AuditEventID,
		RouteAuditEventID:         eventID,
		PermissionMode:            request.PermissionMode,
		ApprovalMode:              request.ApprovalMode,
		WorkspaceMode:             request.WorkspaceMode,
		GatewayToken:              sessionResp.GatewayToken,
	})
}

type captureResponseWriter struct {
	headers http.Header
	body    bytes.Buffer
	status  int
}

func (w *captureResponseWriter) Header() http.Header {
	return w.headers
}

func (w *captureResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *captureResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

func copyCapturedResponse(w http.ResponseWriter, captured *captureResponseWriter) {
	for k, values := range captured.headers {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	status := captured.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(captured.body.Bytes())
}
