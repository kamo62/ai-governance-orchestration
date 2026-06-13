package governance

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

type BackendHandlerConfig struct {
	CurrentBackend string
	GatewayOptions []GatewayOption
	AdminToken     string
	ControlEnabled bool
	WorkDir        string
	Executor       BackendCommandExecutor
}

type BackendCommandExecutor interface {
	Run(context.Context, string, []string, string) (string, error)
}

type ShellBackendCommandExecutor struct{}

func (ShellBackendCommandExecutor) Run(ctx context.Context, name string, args []string, workDir string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

func NewBackendHandler(cfg BackendHandlerConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"current_backend": cfg.CurrentBackend, "options": cfg.GatewayOptions, "commands": backendCommands(), "control_enabled": cfg.ControlEnabled})
			return
		}
		if r.Method != http.MethodPost {
			httpx.WriteJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if cfg.AdminToken == "" || !authorizedBearer(r.Header.Get("Authorization"), cfg.AdminToken) {
			httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "admin access required"})
			return
		}
		if !cfg.ControlEnabled {
			httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"error": "backend control is disabled"})
			return
		}
		var req struct {
			Backend string `json:"backend"`
			Action  string `json:"action"`
		}
		if err := readJSON(w, r, &req); err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
			return
		}
		name, args, err := backendCommand(req.Backend, req.Action)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		exec := cfg.Executor
		if exec == nil {
			exec = ShellBackendCommandExecutor{}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		out, err := exec.Run(ctx, name, args, cfg.WorkDir)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "output": out})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"backend": req.Backend, "action": req.Action, "output": out})
	})
}

func backendCommands() map[string]string {
	return map[string]string{
		"bifrost":      "docker compose -f docker-compose.yml up -d bifrost governance-shell orchestrator",
		"copilot-user": "docker compose -f docker-compose.yml -f docker-compose.copilot.yml up -d governance-shell orchestrator",
	}
}

func backendCommand(backend string, action string) (string, []string, error) {
	if action == "" {
		action = "up"
	}
	if action != "up" && action != "down" {
		return "", nil, fmt.Errorf("action must be up or down")
	}
	base := []string{"compose", "-f", "docker-compose.yml"}
	switch backend {
	case "bifrost":
		if action == "up" {
			return "docker", append(base, "up", "-d", "bifrost", "governance-shell", "orchestrator"), nil
		}
		return "docker", append(base, "stop", "bifrost"), nil
	case "copilot-user":
		args := append(base, "-f", "docker-compose.copilot.yml")
		if action == "up" {
			return "docker", append(args, "up", "-d", "governance-shell", "orchestrator"), nil
		}
		return "docker", append(args, "stop", "governance-shell", "orchestrator"), nil
	default:
		return "", nil, fmt.Errorf("unknown backend %q", backend)
	}
}
