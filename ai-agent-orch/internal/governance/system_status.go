package governance

import "net/http"

type GatewayOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Mode        string `json:"mode"`
	ComposeFile string `json:"compose_file,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

type SystemStatusConfig struct {
	Service               string
	Version               string
	Environment           string
	ModelBackend          string
	GatewayAddr           string
	RuntimeGatewayEnabled bool
	ClassificationMax     string
	PolicyEngine          string
	Gateways              []GatewayOption
}

type SystemStatusHandler struct {
	cfg SystemStatusConfig
}

func NewSystemStatusHandler(cfg SystemStatusConfig) http.Handler {
	return &SystemStatusHandler{cfg: cfg}
}

func (h *SystemStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":                 h.cfg.Service,
		"version":                 h.cfg.Version,
		"environment":             h.cfg.Environment,
		"model_backend":           h.cfg.ModelBackend,
		"model_gateway_addr":      h.cfg.GatewayAddr,
		"runtime_gateway_enabled": h.cfg.RuntimeGatewayEnabled,
		"classification_max":      h.cfg.ClassificationMax,
		"policy_engine":           h.cfg.PolicyEngine,
		"gateways":                h.cfg.Gateways,
	})
}
