package catalog

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type ModelRegistry struct {
	Models []ModelDefinition `yaml:"models"`
}

type ModelDefinition struct {
	Alias                  string            `yaml:"alias"`
	Provider               string            `yaml:"provider"`
	ModelID                string            `yaml:"model_id"`
	Purpose                string            `yaml:"purpose"`
	AllowedClassifications []string          `yaml:"allowed_classifications"`
	FallbackAlias          *string           `yaml:"fallback_alias"`
	Routes                 []ModelRoute      `yaml:"routes"`
	Reasoning              ReasoningMetadata `yaml:"reasoning"`
}

type ModelRoute struct {
	Provider           string            `yaml:"provider"`
	ModelID            string            `yaml:"model_id"`
	CredentialSource   string            `yaml:"credential_source"`
	RequiresActorToken bool              `yaml:"requires_actor_token"`
	Reasoning          ReasoningMetadata `yaml:"reasoning"`
}

type ReasoningMetadata struct {
	DefaultEffort  string `yaml:"default_effort"`
	MaxEffort      string `yaml:"max_effort"`
	SupportsEffort *bool  `yaml:"supports_effort"`
}

// AllowsClassification reports whether the model accepts the given classification.
func (m ModelDefinition) AllowsClassification(classification string) bool {
	if len(m.AllowedClassifications) == 0 {
		return true
	}
	classification = strings.ToLower(strings.TrimSpace(classification))
	for _, c := range m.AllowedClassifications {
		if strings.ToLower(strings.TrimSpace(c)) == classification {
			return true
		}
	}
	return false
}

func (m ModelDefinition) EffectiveRoutes() []ModelRoute {
	if len(m.Routes) > 0 {
		return m.Routes
	}
	return []ModelRoute{{
		Provider:         m.Provider,
		ModelID:          m.ModelID,
		CredentialSource: defaultCredentialSource(m.Provider),
		Reasoning:        m.Reasoning,
	}}
}

func (r ModelRoute) SupportsReasoningEffort() bool {
	if r.Reasoning.SupportsEffort == nil {
		return true
	}
	return *r.Reasoning.SupportsEffort
}

func (m ModelDefinition) SupportsReasoningEffort() bool {
	if m.Reasoning.SupportsEffort == nil {
		return true
	}
	return *m.Reasoning.SupportsEffort
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

func LoadModelRegistry(root string) (ModelRegistry, error) {
	var registry ModelRegistry
	if err := readYAML(filepath.Join(root, "models", "registry.yaml"), &registry); err != nil {
		return ModelRegistry{}, fmt.Errorf("load model registry: %w", err)
	}
	if len(registry.Models) == 0 {
		return ModelRegistry{}, errors.New("model registry has no models")
	}
	return registry, nil
}

func SelectClaudeBackend(registry ModelRegistry, backend string) (ModelRegistry, error) {
	backend = normalizeClaudeBackend(backend)
	if backend == "" {
		return registry, nil
	}
	for i, model := range registry.Models {
		if !isClaudeAliasLane(model) {
			continue
		}
		route, ok := modelRouteForProvider(model.EffectiveRoutes(), backend)
		if !ok {
			return ModelRegistry{}, fmt.Errorf("claude backend %q has no route for alias %q", backend, model.Alias)
		}
		model.Provider = route.Provider
		model.ModelID = route.ModelID
		model.Routes = []ModelRoute{route}
		registry.Models[i] = model
	}
	return registry, nil
}

func normalizeClaudeBackend(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "anthropic":
		return "anthropic"
	case "bedrock", "aws-bedrock", "amazon-bedrock":
		return "bedrock"
	case "foundry", "azure", "azure-ai-foundry", "azure-openai":
		return "foundry"
	default:
		return ""
	}
}

func isClaudeAliasLane(model ModelDefinition) bool {
	if strings.HasPrefix(strings.TrimSpace(model.ModelID), "anthropic/claude") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(model.Provider), "anthropic") && strings.HasPrefix(strings.TrimSpace(model.ModelID), "claude")
}

func modelRouteForProvider(routes []ModelRoute, provider string) (ModelRoute, bool) {
	for _, route := range routes {
		if normalizeClaudeBackend(route.Provider) == provider {
			return route, true
		}
	}
	return ModelRoute{}, false
}

func ResolveOpenRouterModelID(root string, alias string) (string, error) {
	model, err := ResolveModelDefinition(root, alias)
	if err != nil {
		return "", err
	}
	if model.Provider != "openrouter" {
		return "", fmt.Errorf("model alias %q uses provider %q, expected openrouter", alias, model.Provider)
	}
	return model.ModelID, nil
}

func ResolveModelDefinition(root string, alias string) (ModelDefinition, error) {
	if alias == "" {
		return ModelDefinition{}, errors.New("model alias is required")
	}

	registry, err := LoadModelRegistry(root)
	if err != nil {
		return ModelDefinition{}, err
	}
	for _, model := range registry.Models {
		if model.Alias == alias {
			if model.Provider == "" {
				return ModelDefinition{}, fmt.Errorf("model alias %q has no provider", alias)
			}
			if model.ModelID == "" {
				return ModelDefinition{}, fmt.Errorf("model alias %q has no model_id", alias)
			}
			return model, nil
		}
	}
	return ModelDefinition{}, fmt.Errorf("model alias %q not found", alias)
}
