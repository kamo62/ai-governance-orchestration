package orchestrator

import (
	"path/filepath"
	"testing"

	"ai-agent-orch/internal/catalog"
)

func TestRouterGoldenCasesOffline(t *testing.T) {
	root := filepath.Join("..", "..")
	cases, err := catalog.LoadRouterGoldenCases(root)
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
