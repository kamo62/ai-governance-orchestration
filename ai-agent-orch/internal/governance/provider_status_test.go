package governance

import "testing"

func TestProviderReadinessFromEnvDoesNotExposeSecretValues(t *testing.T) {
	env := func(name string) string {
		switch name {
		case "OPENROUTER_API_KEY":
			return "sk-or-v1-secret"
		case "AZURE_AI_FOUNDRY_API_KEY":
			return "foundry-secret"
		case "AZURE_AI_FOUNDRY_ENDPOINT":
			return "https://kamomash-foundry.services.ai.azure.com"
		default:
			return ""
		}
	}
	statuses := ProviderReadinessFromEnv(env, "bifrost", 2)
	byID := map[string]ProviderReadiness{}
	for _, status := range statuses {
		byID[status.ID] = status
		if status.Detail == "sk-or-v1-secret" || status.Detail == "foundry-secret" {
			t.Fatalf("provider readiness leaked secret detail: %#v", status)
		}
	}
	if !byID["openrouter"].Configured || byID["openrouter"].State != "configured" {
		t.Fatalf("expected OpenRouter configured, got %#v", byID["openrouter"])
	}
	if !byID["foundry"].Configured || byID["foundry"].State != "configured" {
		t.Fatalf("expected Foundry configured, got %#v", byID["foundry"])
	}
	if byID["bedrock"].Configured {
		t.Fatalf("did not expect Bedrock configured, got %#v", byID["bedrock"])
	}
	if !ProviderConfiguredForRoute("azure-cognitive-services", statuses) {
		t.Fatalf("expected azure-cognitive-services route to map to Foundry readiness")
	}
	if !byID["copilot-user"].Configured || byID["copilot-user"].EnrollmentCount != 2 {
		t.Fatalf("expected Copilot enrolment summary, got %#v", byID["copilot-user"])
	}
}
