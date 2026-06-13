package governance

import (
	"net/http"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

type ProviderReadiness struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Configured      bool   `json:"configured"`
	State           string `json:"state"`
	Mode            string `json:"mode"`
	Detail          string `json:"detail,omitempty"`
	EnrollmentCount int    `json:"enrollment_count,omitempty"`
}

func ProviderReadinessFromEnv(env func(string) string, modelBackend string, copilotEnrollments int) []ProviderReadiness {
	if env == nil {
		env = func(string) string { return "" }
	}
	configured := func(names ...string) bool {
		for _, name := range names {
			if strings.TrimSpace(env(name)) != "" {
				return true
			}
		}
		return false
	}
	withState := func(id, label, mode string, ok bool, detail string) ProviderReadiness {
		state := "missing"
		if ok {
			state = "configured"
		}
		return ProviderReadiness{ID: id, Label: label, Mode: mode, Configured: ok, State: state, Detail: detail}
	}
	foundryKey := configured("AZURE_AI_FOUNDRY_API_KEY", "AZURE_OPENAI_API_KEY", "AZURE_COGNITIVE_SERVICES_API_KEY")
	foundryEndpoint := configured("AZURE_AI_FOUNDRY_ENDPOINT", "AZURE_OPENAI_ENDPOINT", "AZURE_COGNITIVE_SERVICES_ENDPOINT")
	bedrock := configured("AWS_ACCESS_KEY_ID", "AWS_PROFILE", "AWS_ROLE_ARN")
	copilot := ProviderReadiness{ID: "copilot-user", Label: "GitHub Copilot", Mode: "actor-bound", Configured: copilotEnrollments > 0, State: "missing", EnrollmentCount: copilotEnrollments}
	if copilot.Configured {
		copilot.State = "configured"
		copilot.Detail = "actor enrollments available"
	}
	return []ProviderReadiness{
		withState("openrouter", "OpenRouter", "server-key", configured("OPENROUTER_API_KEY"), "server-side key presence only"),
		withState("foundry", "Azure AI Foundry", "server-key", foundryKey && foundryEndpoint, "server-side key and endpoint presence only"),
		withState("bedrock", "Amazon Bedrock", "server-aws", bedrock, "AWS credential/profile presence only"),
		withState("openai", "OpenAI", "server-key", configured("OPENAI_API_KEY"), "server-side key presence only"),
		withState("anthropic", "Anthropic", "server-key", configured("ANTHROPIC_API_KEY"), "server-side key presence only"),
		withState("deepseek", "DeepSeek", "server-key", configured("DEEPSEEK_API_KEY"), "server-side key presence only"),
		copilot,
	}
}

func ProviderConfiguredForRoute(provider string, statuses []ProviderReadiness) bool {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider == "" {
		return false
	}
	if provider == "copilot-user" || provider == "github-copilot" {
		provider = "copilot-user"
	}
	switch provider {
	case "azure", "azure-openai", "azure-cognitive-services", "azure-ai-foundry", "foundry":
		provider = "foundry"
	case "aws-bedrock", "amazon-bedrock":
		provider = "bedrock"
	}
	for _, status := range statuses {
		if strings.EqualFold(status.ID, provider) {
			return status.Configured
		}
	}
	return false
}

type ProviderStatusHandler struct {
	providers []ProviderReadiness
	load      func() []ProviderReadiness
}

func NewProviderStatusHandler(providers []ProviderReadiness) http.Handler {
	cp := append([]ProviderReadiness(nil), providers...)
	return &ProviderStatusHandler{providers: cp}
}

func NewProviderStatusHandlerFunc(load func() []ProviderReadiness) http.Handler {
	return &ProviderStatusHandler{load: load}
}

func (h *ProviderStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	providers := h.providers
	if h.load != nil {
		providers = h.load()
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"providers": providers})
}
