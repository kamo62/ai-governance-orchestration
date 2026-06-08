package governance

import "time"

// SeedPOCRegistryDefaults registers use-cases and workflows for Bridge/POC demos.
func SeedPOCRegistryDefaults(store RegistryStoreInterface) error {
	if store == nil {
		return nil
	}
	now := time.Now().UTC()
	defaults := []UseCase{
		{
			ID:              "uc-test-generation",
			Owner:           "platform",
			Domain:          "engineering",
			ExpectedBenefit: "Generate governed unit tests with audit evidence",
			Classification:  "internal",
			RiskLevel:       "low",
			CreatedAt:       now,
		},
		{
			ID:              "uc-code-review",
			Owner:           "platform",
			Domain:          "engineering",
			ExpectedBenefit: "Structured code review with policy and cost controls",
			Classification:  "internal",
			RiskLevel:       "medium",
			CreatedAt:       now,
		},
		{
			ID:              "uc-exploratory",
			Owner:           "developer",
			Domain:          "engineering",
			ExpectedBenefit: "Exploratory governed assistance in the IDE",
			Classification:  "internal",
			RiskLevel:       "low",
			CreatedAt:       now,
		},
	}
	for _, uc := range defaults {
		if _, ok := store.GetUseCase(uc.ID); ok {
			continue
		}
		if err := store.RegisterUseCase(uc); err != nil {
			return err
		}
	}

	workflows := []Workflow{
		{
			ID:          "wf-unit-tests",
			Name:        "Unit test generation",
			Description: "Router → unit-tests specialist → patch buffer",
			Stages:      []string{"route", "confirm", "execute", "patch_review"},
			CreatedAt:   now,
		},
		{
			ID:          "wf-code-review",
			Name:        "Code review",
			Description: "Router → code-review specialist",
			Stages:      []string{"route", "confirm", "execute"},
			CreatedAt:   now,
		},
		{
			ID:          "wf-security-review",
			Name:        "Security review",
			Description: "Security-focused governed review workflow",
			Stages:      []string{"route", "confirm", "execute", "evidence"},
			CreatedAt:   now,
		},
	}
	for _, wf := range workflows {
		if _, ok := store.GetWorkflow(wf.ID); ok {
			continue
		}
		if err := store.RegisterWorkflow(wf); err != nil {
			return err
		}
	}
	return nil
}
