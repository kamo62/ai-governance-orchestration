package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// routerGoldenCase is one offline router evaluation example from the
// router-agent catalog evals.
type routerGoldenCase struct {
	Prompt             string `yaml:"prompt"`
	ExpectedSpecialist string `yaml:"expected_specialist"`
}

func loadRouterGoldenCases(root string) ([]routerGoldenCase, error) {
	path := filepath.Join(root, "agents", "core", "router-agent", "evals", "golden-cases.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read router golden cases: %w", err)
	}
	var doc struct {
		Cases []routerGoldenCase `yaml:"cases"`
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

func TestRouterGoldenCasesOffline(t *testing.T) {
	root := filepath.Join("..", "..")
	cases, err := loadRouterGoldenCases(root)
	if err != nil {
		t.Fatalf("load router golden cases: %v", err)
	}

	router := NewRouter(RouterConfig{CatalogRoot: root})
	for _, tc := range cases {
		tc := tc
		t.Run(tc.ExpectedSpecialist, func(t *testing.T) {
			decision, err := router.SelectSpecialist(tc.Prompt, SessionContext{})
			if err != nil {
				t.Fatalf("select specialist: %v", err)
			}
			if decision.Specialist != tc.ExpectedSpecialist {
				t.Fatalf("prompt %q: got specialist %q (%s), want %q", tc.Prompt, decision.Specialist, decision.Reason, tc.ExpectedSpecialist)
			}
		})
	}
}
