package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// openCodePluginSource is the OpenCode plugin that injects the client session id
// and git context headers (and a one-time repo note) for the ai-orch providers.
// It is embedded so `ai-orch opencode` works from any working directory.
//
//go:embed assets/opencode-ai-orch-context.ts
var openCodePluginSource string

const openCodePluginFileName = "ai-orch-context.ts"

// openCodePluginPath resolves the plugin spec to write into an OpenCode config.
// When AI_ORCH_OPENCODE_PLUGIN_PATH is set (for example the docker sandbox mount
// path) it is used verbatim and nothing is written. Otherwise the embedded
// plugin is written into dir and its absolute path is returned, so the config's
// plugin reference resolves regardless of the caller's working directory.
func openCodePluginPath(dir string) (string, error) {
	if override := strings.TrimSpace(os.Getenv("AI_ORCH_OPENCODE_PLUGIN_PATH")); override != "" {
		return override, nil
	}
	if strings.TrimSpace(dir) == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create opencode plugin dir: %w", err)
	}
	target := filepath.Join(dir, openCodePluginFileName)
	if err := os.WriteFile(target, []byte(openCodePluginSource), 0o644); err != nil {
		return "", fmt.Errorf("write opencode plugin: %w", err)
	}
	if abs, err := filepath.Abs(target); err == nil {
		return abs, nil
	}
	return target, nil
}
