package modelgateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/copilot"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/modelbackend"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

func (g *Gateway) modelListContext(r *http.Request) (string, string) {
	classification := ""
	actor := ""

	if sessionID := strings.TrimSpace(r.Header.Get("X-AI-Orch-Session-ID")); sessionID != "" && g.lookupSession != nil {
		if info, err := g.lookupSession(r.Context(), sessionID); err == nil && sessionTokenMatches(r, info.GatewayTokenSHA256, info.RuntimeGatewayTokenSHA256) {
			classification = strings.TrimSpace(info.Classification)
			actor = strings.TrimSpace(info.ActorSubject)
		}
	}
	if classification == "" {
		classification = strings.TrimSpace(r.URL.Query().Get("classification"))
	}
	if classification == "" {
		classification = strings.TrimSpace(r.Header.Get("X-AI-Orch-Classification"))
	}
	if classification == "" {
		classification = "internal"
	}

	if runtimeActor, ok := g.runtimeAuth(r); ok && runtimeActor != "" {
		actor = runtimeActor
	}
	if actor == "" {
		actor = strings.TrimSpace(r.Header.Get("X-AI-Orch-Actor-Subject"))
	}
	if actor == "" {
		actor = strings.TrimSpace(r.Header.Get("X-AI-Orch-Local-Identity"))
	}
	if actor == "" {
		actor, _ = g.runtimeAuth(r)
	}
	return classification, actor
}

func dynamicCopilotClassificationAllowed(classification string) bool {
	switch strings.TrimSpace(strings.ToLower(classification)) {
	case "", "public", "internal":
		return true
	default:
		return false
	}
}

func (g *Gateway) dynamicCopilotPickerModels(ctx context.Context, actor string) ([]copilot.PickerModel, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return nil, nil
	}
	lister, ok := g.backend.(modelbackend.ModelCatalogBackend)
	if !ok {
		return nil, nil
	}
	body, err := lister.Models(ctx, actor)
	if err != nil {
		return nil, err
	}
	return copilot.PickerChatModelsFromCatalog(body)
}

func (g *Gateway) appendDynamicCopilotModelEntries(ctx context.Context, actor string, classification string, data []modelListEntry) []modelListEntry {
	if !dynamicCopilotClassificationAllowed(classification) {
		return data
	}
	models, err := g.dynamicCopilotPickerModels(ctx, actor)
	if err != nil {
		return data
	}
	seen := make(map[string]bool, len(data)+len(models))
	for _, entry := range data {
		seen[entry.ID] = true
	}
	for _, model := range models {
		if seen[model.Alias] {
			continue
		}
		data = append(data, modelListEntry{
			ID:      model.Alias,
			Object:  "model",
			OwnedBy: "ai-orch",
			Name:    "Governed Copilot " + model.DisplayName,
		})
		seen[model.Alias] = true
	}
	return data
}

func (g *Gateway) routeModel(ctx context.Context, preferredAlias string, session SessionInfo, taskType string) (router.Decision, error) {
	decision, routeErr := g.router.Route(ctx, router.Request{
		TaskType:       taskType,
		Classification: session.Classification,
		PreferredAlias: preferredAlias,
		ActorSubject:   session.ActorSubject,
	})
	if routeErr == nil {
		return decision, nil
	}
	if decision, ok, err := g.dynamicCopilotDecision(ctx, preferredAlias, session.ActorSubject, session.Classification); ok || err != nil {
		return decision, err
	}
	return router.Decision{}, routeErr
}

func (g *Gateway) dynamicCopilotDecision(ctx context.Context, alias string, actor string, classification string) (router.Decision, bool, error) {
	alias = strings.TrimSpace(alias)
	if !strings.HasPrefix(strings.ToLower(alias), "copilot-") {
		return router.Decision{}, false, nil
	}
	if !dynamicCopilotClassificationAllowed(classification) {
		return router.Decision{}, false, nil
	}
	lister, ok := g.backend.(modelbackend.ModelCatalogBackend)
	if !ok {
		return router.Decision{}, false, nil
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return router.Decision{}, true, fmt.Errorf("actor identity is required for dynamic Copilot model alias %q", alias)
	}
	body, err := lister.Models(ctx, actor)
	if err != nil {
		return router.Decision{}, true, fmt.Errorf("dynamic Copilot model lookup failed for actor %q: %w", actor, err)
	}
	model, ok, err := copilot.FindPickerChatModelByAlias(body, alias)
	if err != nil {
		return router.Decision{}, true, fmt.Errorf("parse dynamic Copilot model catalog: %w", err)
	}
	if !ok {
		return router.Decision{}, false, nil
	}
	return router.Decision{
		SelectedAlias:    model.Alias,
		RequestedAlias:   alias,
		SelectedModelID:  model.ModelID,
		Provider:         modelbackend.BackendCopilotUser,
		CredentialSource: modelbackend.BackendCopilotUser,
		Reasons:          []string{"actor-bound Copilot model discovered from Copilot model catalog"},
		CostPosture:      "controlled",
		LatencyPosture:   "balanced",
	}, true, nil
}
