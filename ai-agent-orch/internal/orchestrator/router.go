package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"ai-agent-orch/internal/audit"
	"ai-agent-orch/internal/catalog"
)

type RouterConfig struct {
	CatalogRoot string
	Audit       AuditStore
	NewID       func(prefix string) string
}

type Router struct {
	catalogRoot string
	audit       AuditStore
	newID       func(prefix string) string
}

type RouteRequest struct {
	Prompt string `json:"prompt"`
}

type RouteDecision struct {
	Specialist string `json:"specialist"`
	Reason     string `json:"reason"`
}

type RouteResponse struct {
	SessionID    string `json:"session_id"`
	Status       string `json:"status"`
	Specialist   string `json:"specialist"`
	Reason       string `json:"reason"`
	AuditEventID string `json:"audit_event_id,omitempty"`
}

func NewRouter(cfg RouterConfig) *Router {
	newID := cfg.NewID
	if newID == nil {
		newID = randomID
	}
	root := cfg.CatalogRoot
	if root == "" {
		root = "."
	}
	return &Router{
		catalogRoot: root,
		audit:       cfg.Audit,
		newID:       newID,
	}
}

func NewRouterHandler(router *Router) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orchestrator/route", router.route)
	return mux
}

func (r *Router) SelectSpecialist(prompt string) (RouteDecision, error) {
	if prompt == "" {
		return RouteDecision{}, errors.New("prompt is required")
	}
	report, err := catalog.Validate(r.catalogRoot)
	if err != nil {
		return RouteDecision{}, fmt.Errorf("validate catalog: %w", err)
	}
	candidate, reason := selectByKeywords(prompt)
	if !report.HasAgent(candidate) {
		return RouteDecision{}, fmt.Errorf("selected specialist %q is not in catalog", candidate)
	}
	return RouteDecision{
		Specialist: candidate,
		Reason:     reason,
	}, nil
}

func (r *Router) route(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if r == nil || r.audit == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "router unavailable"})
		return
	}
	sessionID := req.Header.Get("X-AI-Orch-Session-ID")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "X-AI-Orch-Session-ID header is required"})
		return
	}

	var request RouteRequest
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	decision, err := r.SelectSpecialist(request.Prompt)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	eventID := r.newID("evt")
	promptHash := sha256.Sum256([]byte(request.Prompt))
	event, err := r.audit.Append(req.Context(), audit.Event{
		EventID:            eventID,
		SessionID:          sessionID,
		EventType:          "router.specialist.selected",
		Actor:              "local-dev",
		Agent:              decision.Specialist,
		Reason:             decision.Reason,
		PromptSHA256:       hex.EncodeToString(promptHash[:]),
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "orchestrator",
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}

	writeJSON(w, http.StatusOK, RouteResponse{
		SessionID:    sessionID,
		Status:       "selected",
		Specialist:   decision.Specialist,
		Reason:       decision.Reason,
		AuditEventID: event.EventID,
	})
}

func selectByKeywords(prompt string) (string, string) {
	text := strings.ToLower(prompt)
	switch {
	case containsAny(text, "playwright", "unit test", "integration test", "regression", "coverage", "test", "tests"):
		return "test-generation", "testing keyword match"
	case containsAny(text, "secret", "auth", "authentication", "authorization", "security", "vulnerability", "dependency risk", "data exposure"):
		return "security-scan", "security keyword match"
	case containsAny(text, "architecture", "service boundaries", "boundaries", "data flow", "design review", "deployment shape", "tradeoff"):
		return "architecture-review", "architecture keyword match"
	case containsAny(text, "readme", "documentation", "docs", "developer notes", "api explanation"):
		return "documentation", "documentation keyword match"
	case containsAny(text, "refactor", "cleanup", "clean up", "reorganize", "simplify", "rename"):
		return "refactor", "refactor keyword match"
	case containsAny(text, "review", "diff", "bugs", "risky", "pr review", "regression"):
		return "code-review", "code review keyword match"
	default:
		return "code-review", "default review specialist"
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
