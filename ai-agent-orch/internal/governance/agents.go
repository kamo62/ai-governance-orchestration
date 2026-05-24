package governance

import (
	"net/http"
	"path/filepath"

	"ai-agent-orch/internal/catalog"
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
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	report, err := catalog.Validate(h.catalogRoot)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agents": report.Agents,
	})
}

// rel returns the path relative to the catalog root for display.
func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}
