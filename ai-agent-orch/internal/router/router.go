// Package router implements the Governance Router that selects model aliases
// based on task context, classification, cost, risk, and evidence needs.
package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
)

// Request is the input to a routing decision.
type Request struct {
	TaskType           string // e.g. "coding", "review", "test", "architecture"
	Classification     string // e.g. "public", "internal", "confidential", "restricted"
	WorkflowStage      string // e.g. "draft", "review", "verify", "merge"
	RiskLevel          string // e.g. "low", "medium", "high"
	CostSensitivity    string // e.g. "low", "medium", "high"
	LatencySensitivity string // e.g. "low", "medium", "high"
	EvidenceNeeds      string // e.g. "none", "basic", "full"
	PreferredAlias     string // optional user preference; validated, not blindly trusted
	ActorSubject       string // actor identity used for actor-bound route availability
}

// Decision is the output of a routing decision.
type Decision struct {
	SelectedAlias            string   `json:"selected_alias"`
	SelectedModelID          string   `json:"selected_model_id"`
	Provider                 string   `json:"provider"`
	Reasons                  []string `json:"reasons"`
	FallbackChain            []string `json:"fallback_chain,omitempty"`
	RejectedAliases          []string `json:"rejected_aliases,omitempty"`
	CostPosture              string   `json:"cost_posture,omitempty"`
	LatencyPosture           string   `json:"latency_posture,omitempty"`
	RequestedAlias           string   `json:"requested_alias,omitempty"`
	CredentialSource         string   `json:"credential_source,omitempty"`
	ReasoningDefaultEffort   string   `json:"reasoning_default_effort,omitempty"`
	ReasoningMaxEffort       string   `json:"reasoning_max_effort,omitempty"`
	ReasoningSupportsEffort  bool     `json:"reasoning_supports_effort"`
	ReasoningEffortRequested string   `json:"reasoning_effort_requested,omitempty"`
	ReasoningEffortApplied   string   `json:"reasoning_effort_applied,omitempty"`
	ReasoningSource          string   `json:"reasoning_source,omitempty"`
}

type RouteAvailability func(context.Context, catalog.ModelRoute, Request) bool

// Router selects model aliases from the catalog registry based on governance context.
type Router struct {
	registry       catalog.ModelRegistry
	routeAvailable RouteAvailability
}

// New creates a Router from the catalog registry loaded at the given root.
func New(registry catalog.ModelRegistry) *Router {
	return &Router{registry: registry}
}

func NewWithRouteAvailability(registry catalog.ModelRegistry, routeAvailable RouteAvailability) *Router {
	return &Router{registry: registry, routeAvailable: routeAvailable}
}

// Route selects the best model alias for the given request.
// It filters by classification compatibility, then scores by task alignment,
// workflow stage, risk, cost sensitivity, latency sensitivity and evidence needs.
func (r *Router) Route(ctx context.Context, req Request) (Decision, error) {
	if r == nil || len(r.registry.Models) == 0 {
		return Decision{}, errors.New("router not configured")
	}

	decision := Decision{
		Reasons:        []string{},
		CostPosture:    "controlled",
		LatencyPosture: "balanced",
		RequestedAlias: req.PreferredAlias,
	}

	// Build candidate list filtered by classification.
	var candidates []catalog.ModelDefinition
	for _, m := range r.registry.Models {
		if !m.AllowsClassification(req.Classification) {
			decision.RejectedAliases = append(decision.RejectedAliases, m.Alias)
			continue
		}
		candidates = append(candidates, m)
	}

	if len(candidates) == 0 {
		return Decision{}, fmt.Errorf("no models available for classification %q", req.Classification)
	}

	// If a preferred alias is provided, validate it.
	if req.PreferredAlias != "" {
		for _, m := range candidates {
			if m.Alias == req.PreferredAlias {
				if err := r.applyRoute(ctx, &decision, m, req); err != nil {
					return Decision{}, err
				}
				decision.Reasons = append(decision.Reasons, fmt.Sprintf("preferred alias %q accepted", m.Alias))
				decision.FallbackChain = buildFallbackChain(r.registry, m, req.Classification)
				// Override postures based on request context even for preferred aliases.
				decision.CostPosture = costPostureFromRequest(req)
				decision.LatencyPosture = latencyPostureFromRequest(req)
				return decision, nil
			}
		}
		return Decision{}, fmt.Errorf("preferred alias %q not found or not allowed for classification %q", req.PreferredAlias, req.Classification)
	}

	// Score candidates by task alignment.
	best := selectBestCandidate(candidates, req)
	if best.Alias == "" {
		return Decision{}, errors.New("no suitable model found after scoring")
	}

	if err := r.applyRoute(ctx, &decision, best, req); err != nil {
		return Decision{}, err
	}
	decision.Reasons = append(decision.Reasons, fmt.Sprintf("selected by task alignment: %s", best.Purpose))
	decision.FallbackChain = buildFallbackChain(r.registry, best, req.Classification)
	decision.CostPosture = costPostureFromRequest(req)
	decision.LatencyPosture = latencyPostureFromRequest(req)

	// Enrich reasons with workflow and risk context.
	if req.WorkflowStage != "" {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("workflow_stage: %s", req.WorkflowStage))
	}
	if req.RiskLevel != "" {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("risk_level: %s", req.RiskLevel))
	}
	if req.EvidenceNeeds == "full" {
		decision.Reasons = append(decision.Reasons, "evidence_required")
	}

	return decision, nil
}

// Resolve returns the concrete provider model ID for a given alias.
func (r *Router) Resolve(alias string) (modelID string, provider string, err error) {
	for _, m := range r.registry.Models {
		if m.Alias != alias {
			continue
		}
		route, ok := r.selectRoute(context.Background(), m, Request{})
		if !ok || route.ModelID == "" {
			return "", "", fmt.Errorf("alias %q has no model_id", alias)
		}
		return route.ModelID, route.Provider, nil
	}
	return "", "", fmt.Errorf("alias %q not found", alias)
}

// Aliases returns all governed aliases, optionally filtered by classification.
func (r *Router) Aliases(classification string) []catalog.ModelDefinition {
	var out []catalog.ModelDefinition
	for _, m := range r.registry.Models {
		if classification == "" || m.AllowsClassification(classification) {
			out = append(out, m)
		}
	}
	return out
}

func (r *Router) applyRoute(ctx context.Context, decision *Decision, model catalog.ModelDefinition, req Request) error {
	route, ok := r.selectRoute(ctx, model, req)
	if !ok {
		return fmt.Errorf("alias %q has no available route for actor %q", model.Alias, req.ActorSubject)
	}
	decision.SelectedAlias = model.Alias
	decision.SelectedModelID = route.ModelID
	decision.Provider = route.Provider
	decision.CredentialSource = strings.TrimSpace(route.CredentialSource)
	if decision.CredentialSource == "" {
		decision.CredentialSource = defaultCredentialSource(route.Provider)
	}
	decision.ReasoningDefaultEffort = normalizeEffort(route.Reasoning.DefaultEffort)
	decision.ReasoningMaxEffort = normalizeEffort(route.Reasoning.MaxEffort)
	decision.ReasoningSupportsEffort = route.SupportsReasoningEffort()
	return nil
}

func (r *Router) selectRoute(ctx context.Context, model catalog.ModelDefinition, req Request) (catalog.ModelRoute, bool) {
	for _, route := range model.EffectiveRoutes() {
		route.Provider = strings.TrimSpace(route.Provider)
		route.ModelID = strings.TrimSpace(route.ModelID)
		if route.Provider == "" || route.ModelID == "" {
			continue
		}
		if route.CredentialSource == "" {
			route.CredentialSource = defaultCredentialSource(route.Provider)
		}
		if route.Reasoning.DefaultEffort == "" {
			route.Reasoning.DefaultEffort = model.Reasoning.DefaultEffort
		}
		if route.Reasoning.MaxEffort == "" {
			route.Reasoning.MaxEffort = model.Reasoning.MaxEffort
		}
		if route.Reasoning.SupportsEffort == nil {
			route.Reasoning.SupportsEffort = model.Reasoning.SupportsEffort
		}
		if route.RequiresActorToken {
			if strings.TrimSpace(req.ActorSubject) == "" {
				continue
			}
			if r.routeAvailable != nil && !r.routeAvailable(ctx, route, req) {
				continue
			}
		}
		return route, true
	}
	return catalog.ModelRoute{}, false
}

func selectBestCandidate(candidates []catalog.ModelDefinition, req Request) catalog.ModelDefinition {
	if len(candidates) == 0 {
		return catalog.ModelDefinition{}
	}

	var best catalog.ModelDefinition
	bestScore := -1

	for _, m := range candidates {
		score := scoreCandidate(m, req)
		if score > bestScore {
			bestScore = score
			best = m
		}
	}

	return best
}

func scoreCandidate(m catalog.ModelDefinition, req Request) int {
	score := 0
	purpose := strings.ToLower(m.Purpose)
	task := strings.ToLower(req.TaskType)
	risk := strings.ToLower(req.RiskLevel)
	workflow := strings.ToLower(req.WorkflowStage)

	// Task type alignment
	switch task {
	case "coding", "implementation":
		if strings.Contains(purpose, "coding") || strings.Contains(purpose, "implementation") {
			score += 10
		}
	case "review", "audit":
		if strings.Contains(purpose, "review") || strings.Contains(purpose, "audit") || strings.Contains(purpose, "quality") {
			score += 10
		}
	case "test", "testing":
		if strings.Contains(purpose, "test") {
			score += 10
		}
	case "architecture", "design":
		if strings.Contains(purpose, "architecture") || strings.Contains(purpose, "design") {
			score += 10
		}
	case "routing", "summarization":
		if strings.Contains(purpose, "routing") || strings.Contains(purpose, "summarization") || strings.Contains(purpose, "classification") {
			score += 10
		}
	}

	// Workflow stage alignment
	switch workflow {
	case "draft", "investigate":
		if strings.Contains(purpose, "fast") || strings.Contains(purpose, "economy") {
			score += 4
		}
	case "review", "verify":
		if strings.Contains(purpose, "highest-quality") || strings.Contains(purpose, "security") || strings.Contains(purpose, "review") {
			score += 4
		}
	case "execute", "merge":
		if strings.Contains(purpose, "coding") || strings.Contains(purpose, "implementation") {
			score += 4
		}
	}

	// Risk level alignment
	if risk == "high" {
		if strings.Contains(purpose, "highest-quality") || strings.Contains(purpose, "security") || strings.Contains(purpose, "deep") {
			score += 5
		}
	} else if risk == "low" {
		if strings.Contains(purpose, "fast") || strings.Contains(purpose, "economy") || strings.Contains(purpose, "cheap") {
			score += 5
		}
	}

	// Cost sensitivity
	if strings.ToLower(req.CostSensitivity) == "high" {
		if strings.Contains(purpose, "economy") || strings.Contains(purpose, "cheap") || strings.Contains(purpose, "fast") {
			score += 3
		}
	} else if strings.ToLower(req.CostSensitivity) == "low" {
		if strings.Contains(purpose, "highest-quality") || strings.Contains(purpose, "deep") {
			score += 3
		}
	}

	// Latency sensitivity
	if strings.ToLower(req.LatencySensitivity) == "high" {
		if strings.Contains(purpose, "fast") || strings.Contains(purpose, "economy") {
			score += 3
		}
	} else if strings.ToLower(req.LatencySensitivity) == "low" {
		if strings.Contains(purpose, "highest-quality") || strings.Contains(purpose, "deep") || strings.Contains(purpose, "reasoning") {
			score += 3
		}
	}

	// Evidence needs
	if strings.ToLower(req.EvidenceNeeds) == "full" {
		if strings.Contains(purpose, "highest-quality") || strings.Contains(purpose, "security") || strings.Contains(purpose, "review") {
			score += 3
		}
	}

	return score
}

func costPostureFromRequest(req Request) string {
	switch strings.ToLower(req.CostSensitivity) {
	case "high":
		return "economy"
	case "low":
		return "performance"
	default:
		return "controlled"
	}
}

func latencyPostureFromRequest(req Request) string {
	switch strings.ToLower(req.LatencySensitivity) {
	case "high":
		return "fast"
	case "low":
		return "thorough"
	default:
		return "balanced"
	}
}

func buildFallbackChain(registry catalog.ModelRegistry, start catalog.ModelDefinition, classification string) []string {
	var chain []string
	seen := map[string]struct{}{start.Alias: {}}
	current := start.FallbackAlias
	for current != nil && *current != "" {
		if _, ok := seen[*current]; ok {
			break // cycle detected
		}
		seen[*current] = struct{}{}
		// Find next fallback
		found := false
		for _, m := range registry.Models {
			if m.Alias == *current {
				if !m.AllowsClassification(classification) {
					return chain
				}
				chain = append(chain, *current)
				current = m.FallbackAlias
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	return chain
}

func defaultCredentialSource(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "copilot-user":
		return "copilot-user"
	case "":
		return ""
	default:
		return "platform-" + strings.ToLower(strings.TrimSpace(provider))
	}
}

func normalizeEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return ""
	}
}
