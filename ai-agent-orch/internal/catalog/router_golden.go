package catalog

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// RouterGoldenCase is one offline router evaluation example.
type RouterGoldenCase struct {
	Prompt             string `yaml:"prompt"`
	ExpectedSpecialist string `yaml:"expected_specialist"`
}

// LoadRouterGoldenCases reads router-agent golden routing cases from the catalog.
func LoadRouterGoldenCases(root string) ([]RouterGoldenCase, error) {
	path := filepath.Join(root, "agents", "core", "router-agent", "evals", "golden-cases.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read router golden cases: %w", err)
	}
	var doc struct {
		Cases []RouterGoldenCase `yaml:"cases"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse router golden cases: %w", err)
	}
	if len(doc.Cases) == 0 {
		return nil, fmt.Errorf("router golden cases are empty")
	}
	for i, c := range doc.Cases {
		if c.Prompt == "" {
			return nil, fmt.Errorf("router golden case %d: prompt is required", i+1)
		}
		if c.ExpectedSpecialist == "" {
			return nil, fmt.Errorf("router golden case %d: expected_specialist is required", i+1)
		}
	}
	return doc.Cases, nil
}
