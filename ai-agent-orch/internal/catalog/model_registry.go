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
	Alias                  string   `yaml:"alias"`
	Provider               string   `yaml:"provider"`
	ModelID                string   `yaml:"model_id"`
	Purpose                string   `yaml:"purpose"`
	AllowedClassifications []string `yaml:"allowed_classifications"`
	FallbackAlias          *string  `yaml:"fallback_alias"`
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

func ResolveOpenRouterModelID(root string, alias string) (string, error) {
	if alias == "" {
		return "", errors.New("model alias is required")
	}

	registry, err := LoadModelRegistry(root)
	if err != nil {
		return "", err
	}
	for _, model := range registry.Models {
		if model.Alias == alias {
			if model.Provider != "openrouter" {
				return "", fmt.Errorf("model alias %q uses provider %q, expected openrouter", alias, model.Provider)
			}
			if model.ModelID == "" {
				return "", fmt.Errorf("model alias %q has no model_id", alias)
			}
			return model.ModelID, nil
		}
	}
	return "", fmt.Errorf("model alias %q not found", alias)
}
