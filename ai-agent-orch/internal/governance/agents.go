package governance

import (
	"net/http"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

// AgentListHandler serves GET /v1/agents.
type AgentListHandler struct {
	catalogRoot string
}

func NewAgentListHandler(catalogRoot string) http.Handler {
	return &AgentListHandler{catalogRoot: catalogRoot}
}

func (h *AgentListHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	report, err := catalog.Validate(h.catalogRoot)
	if err != nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"agents": report.Agents,
	})
}
