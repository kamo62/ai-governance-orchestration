package governance

import (
	"errors"
	"net/http"
	"strings"

	"ai-agent-orch/internal/composition"
)

// CompositionHandler serves composition lifecycle endpoints.
type CompositionHandler struct {
	service *SessionService
	store   *composition.CompositionStore
}

func NewCompositionHandler(service *SessionService, store *composition.CompositionStore) http.Handler {
	return &CompositionHandler{service: service, store: store}
}

func (h *CompositionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.service == nil || h.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "composition handler unavailable"})
		return
	}

	authReq, ok := h.service.RequireAuthorizedRequest(w, r)
	if !ok {
		return
	}
	r = authReq

	switch r.Method {
	case http.MethodPost:
		if r.URL.Path == "/v1/compositions" {
			h.createComposition(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/approve") {
			h.approveStage(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/advance") {
			h.advanceComposition(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/complete") {
			h.completeStage(w, r)
			return
		}
	case http.MethodGet:
		if strings.HasPrefix(r.URL.Path, "/v1/compositions/") {
			h.getComposition(w, r)
			return
		}
	}
	writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
}

func (h *CompositionHandler) createComposition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID   string              `json:"session_id"`
		Description string              `json:"description,omitempty"`
		Stages      []composition.Stage `json:"stages"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if req.SessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id is required"})
		return
	}
	if len(req.Stages) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "stages are required"})
		return
	}
	if err := composition.ValidateStages(req.Stages, 2); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if !h.requireSessionOwner(w, r, req.SessionID) {
		return
	}

	comp := composition.NewComposition(req.SessionID, req.Stages)
	comp.Description = req.Description
	h.store.Set(req.SessionID, comp)

	writeJSON(w, http.StatusCreated, map[string]any{
		"session_id":  req.SessionID,
		"status":      "created",
		"stages":      comp.Stages,
		"current_idx": comp.CurrentIdx,
		"max_depth":   comp.MaxDepth,
	})
}

func (h *CompositionHandler) getComposition(w http.ResponseWriter, r *http.Request) {
	sessionID := extractSessionID(r.URL.Path, "/v1/compositions/", "")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	if !h.requireSessionOwner(w, r, sessionID) {
		return
	}
	comp, ok := h.store.Get(sessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "composition not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":  comp.SessionID,
		"stages":      comp.Stages,
		"current_idx": comp.CurrentIdx,
		"max_depth":   comp.MaxDepth,
		"complete":    comp.IsComplete(),
	})
}

func (h *CompositionHandler) approveStage(w http.ResponseWriter, r *http.Request) {
	sessionID := extractSessionID(r.URL.Path, "/v1/compositions/", "/approve")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	if !h.requireSessionOwner(w, r, sessionID) {
		return
	}

	comp, ok, err := h.store.Update(sessionID, func(comp *composition.Composition) error {
		return comp.ApproveStage()
	})
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "composition not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":  sessionID,
		"status":      "stage_approved",
		"current_idx": comp.CurrentIdx,
	})
}

func (h *CompositionHandler) advanceComposition(w http.ResponseWriter, r *http.Request) {
	sessionID := extractSessionID(r.URL.Path, "/v1/compositions/", "/advance")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	if !h.requireSessionOwner(w, r, sessionID) {
		return
	}

	comp, ok, err := h.store.Update(sessionID, func(comp *composition.Composition) error {
		return comp.Advance()
	})
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "composition not found"})
		return
	}
	if err != nil {
		if errors.Is(err, composition.ErrHumanGateRequired) {
			writeJSON(w, http.StatusLocked, map[string]any{"error": err.Error()})
			return
		}
		if errors.Is(err, composition.ErrMaxDepthExceeded) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":  sessionID,
		"status":      "advanced",
		"current_idx": comp.CurrentIdx,
		"stage":       comp.Stages[comp.CurrentIdx],
	})
}

func (h *CompositionHandler) completeStage(w http.ResponseWriter, r *http.Request) {
	sessionID := extractSessionID(r.URL.Path, "/v1/compositions/", "/complete")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "valid session ID is required"})
		return
	}

	var req struct {
		Output string `json:"output"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}

	if !h.requireSessionOwner(w, r, sessionID) {
		return
	}

	comp, ok, err := h.store.Update(sessionID, func(comp *composition.Composition) error {
		return comp.CompleteStage(req.Output)
	})
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "composition not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":  sessionID,
		"status":      "stage_completed",
		"current_idx": comp.CurrentIdx,
		"output":      req.Output,
	})
}

func (h *CompositionHandler) requireSessionOwner(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if h.service == nil || h.service.sessions == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "session store unavailable"})
		return false
	}
	record, err := h.service.sessions.Get(r.Context(), sessionID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
		return false
	}
	if record.ActorSubject != actorFromContext(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "session ownership mismatch"})
		return false
	}
	return true
}
