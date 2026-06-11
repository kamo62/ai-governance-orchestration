package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/classifier"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
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
	cacheMu     sync.RWMutex
	cached      *cachedValidation
}

type cachedValidation struct {
	report      catalog.Report
	err         error
	validatedAt time.Time
}

type SessionContext struct {
	RepoURL      string `json:"repo_url,omitempty"`
	Branch       string `json:"branch,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	WorkItemID   string `json:"work_item_id,omitempty"`
	WorkItemType string `json:"work_item_type,omitempty"`
	ActorHint    string `json:"actor_hint,omitempty"`
	SourceSystem string `json:"source_system,omitempty"`
}

type RouteRequest struct {
	Prompt  string         `json:"prompt"`
	Context SessionContext `json:"context,omitempty"`
}

type RouteDecision struct {
	Specialist string `json:"specialist"`
	Reason     string `json:"reason"`
	// Classification is advisory governance-router metadata (task type, workflow,
	// risk, model route, evidence). It enriches the decision for audit and client
	// display. It never changes specialist selection and is not authoritative for
	// the data-classification ceiling, which stays caller-supplied and enforced
	// server-side by the Governance Shell.
	Classification *classifier.Result `json:"classification,omitempty"`
}

type RouteResponse struct {
	SessionID      string             `json:"session_id"`
	Status         string             `json:"status"`
	Specialist     string             `json:"specialist"`
	Reason         string             `json:"reason"`
	Classification *classifier.Result `json:"classification,omitempty"`
	AuditEventID   string             `json:"audit_event_id,omitempty"`
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

func (r *Router) SelectSpecialist(prompt string, ctx SessionContext) (RouteDecision, error) {
	if prompt == "" {
		return RouteDecision{}, errors.New("prompt is required")
	}
	report, err := r.cachedCatalog()
	if err != nil {
		return RouteDecision{}, fmt.Errorf("validate catalog: %w", err)
	}

	// Advisory classification enrichment. Deterministic (nil LLM), never errors,
	// and never overrides the specialist selection below.
	classification, _ := classifier.New(nil).Classify(context.Background(), classifier.Request{
		Prompt:       prompt,
		RepoURL:      ctx.RepoURL,
		Branch:       ctx.Branch,
		CommitSHA:    ctx.CommitSHA,
		WorkItemID:   ctx.WorkItemID,
		WorkItemType: ctx.WorkItemType,
		ActorHint:    ctx.ActorHint,
		SourceSystem: ctx.SourceSystem,
	})

	// 1. Branch prefix routing (highest confidence).
	if ctx.Branch != "" {
		candidate := r.agentForBranch(ctx.Branch, ctx.WorkItemType, report)
		if candidate != "" {
			return RouteDecision{
				Specialist:     candidate,
				Reason:         "branch_prefix:" + ctx.WorkItemType,
				Classification: &classification,
			}, nil
		}
	}

	// 2. Keyword fallback.
	candidate, reason := selectByKeywords(prompt)
	if !report.HasAgent(candidate) {
		return RouteDecision{}, fmt.Errorf("selected specialist %q is not in catalog", candidate)
	}
	return RouteDecision{
		Specialist:     candidate,
		Reason:         reason,
		Classification: &classification,
	}, nil
}

func (r *Router) agentForBranch(branch, workItemType string, report catalog.Report) string {
	// Map work item type to agent.
	typeMap := map[string]string{
		"frontend": "frontend-development",
		"backend":  "backend-development",
		"bugfix":   "code-review",
		"docs":     "documentation",
		"refactor": "refactor",
		"test":     "unit-tests",
		"security": "security-scan",
	}
	if agent, ok := typeMap[workItemType]; ok && report.HasAgent(agent) {
		return agent
	}
	// If no work item type, infer from branch prefix.
	parts := strings.Split(branch, "/")
	if len(parts) >= 2 {
		if agent, ok := typeMap[strings.ToLower(parts[0])]; ok && report.HasAgent(agent) {
			return agent
		}
	}
	return ""
}

// cachedCatalog returns a validated catalog report, using a time-based cache
// to avoid re-reading the full catalog from disk on every route request.
// If validation fails, the error is cached for a shorter window so that
// subsequent requests fail closed quickly without hammering the filesystem.
func (r *Router) cachedCatalog() (catalog.Report, error) {
	r.cacheMu.RLock()
	cached := r.cached
	r.cacheMu.RUnlock()

	const hitTTL = 30 * time.Second
	const errTTL = 5 * time.Second

	if cached != nil {
		age := time.Since(cached.validatedAt)
		if cached.err == nil && age < hitTTL {
			return cached.report, nil
		}
		if cached.err != nil && age < errTTL {
			return catalog.Report{}, cached.err
		}
	}

	report, err := catalog.Validate(r.catalogRoot)

	r.cacheMu.Lock()
	r.cached = &cachedValidation{
		report:      report,
		err:         err,
		validatedAt: time.Now(),
	}
	r.cacheMu.Unlock()

	if err != nil {
		return catalog.Report{}, err
	}
	return report, nil
}

func (r *Router) route(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if r == nil || r.audit == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "router unavailable"})
		return
	}
	sessionID := req.Header.Get("X-AI-Orch-Session-ID")
	if sessionID == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "X-AI-Orch-Session-ID header is required"})
		return
	}

	var request RouteRequest
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&request); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body"})
		return
	}
	decision, err := r.SelectSpecialist(request.Prompt, request.Context)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
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
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "audit write failed"})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, RouteResponse{
		SessionID:      sessionID,
		Status:         "selected",
		Specialist:     decision.Specialist,
		Reason:         decision.Reason,
		Classification: decision.Classification,
		AuditEventID:   event.EventID,
	})
}

func selectByKeywords(prompt string) (string, string) {
	text := strings.ToLower(prompt)
	switch {
	case containsAny(text, "terraform", "tf module", " hcl", "infrastructure as code"):
		return "terraform-review", "terraform keyword match"
	case containsAny(text, "full security review", "security review of this codebase", "security review of the codebase"):
		return "security-review", "security review keyword match"
	case containsAny(text, "playwright", "unit test", "integration test", "regression", "coverage") ||
		(containsAny(text, "test", "tests") && !containsAny(text, "contest", "latest")):
		return "unit-tests", "testing keyword match"
	case containsAny(text, "react", "frontend", "typescript and css", "vue", "angular", "svelte"):
		return "frontend-development", "frontend keyword match"
	case containsAny(text, "go http", "http handler", "rest api endpoint", "golang", "backend service", "grpc"):
		return "backend-development", "backend keyword match"
	case containsAny(text, "secret", "auth", "authentication", "authorization", "vulnerability", "dependency risk", "data exposure"):
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
