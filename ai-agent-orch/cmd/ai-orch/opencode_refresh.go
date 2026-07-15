package main

import (
	"context"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func refreshOpenCodeConfig(ctx context.Context, cfg Config, gatewayURL string, args []string) error {
	fs := flag.NewFlagSet("opencode refresh", flag.ContinueOnError)
	scope := fs.String("scope", "global", "OpenCode config scope: global or project")
	configPath := fs.String("path", "", "explicit OpenCode config path")
	classification := fs.String("classification", "internal", "classification header for AI-Orch-routed OpenCode")
	installJob := fs.Bool("install-refresh-job", false, "install or update the user-level refresh job")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cred, err := requestDeveloperRuntimeCredential(ctx, cfg, "opencode")
	if err != nil {
		return fmt.Errorf("refresh runtime credential: %w", err)
	}
	installArgs := []string{"--scope", *scope, "--force", "--runtime-token", cred.RuntimeToken, "--actor-subject", cred.ActorSubject, "--classification", *classification}
	if *configPath != "" {
		installArgs = append(installArgs, "--path", *configPath)
	}
	if err := installOpenCodeConfig(gatewayURL, installArgs); err != nil {
		return err
	}
	if *installJob {
		path, err := writeOpenCodeRefreshJob(runtime.GOOS, defaultOpenCodeRefreshCommand(*scope, *configPath))
		if err != nil {
			return err
		}
		if path != "" {
			fmt.Printf("AI-Orch-routed OpenCode refresh job installed: %s\n", path)
		}
	}
	fmt.Printf("AI-Orch-routed OpenCode config refreshed for %s\n", cred.ActorSubject)
	return nil
}

func defaultOpenCodeRefreshCommand(scope string, configPath string) []string {
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		exe = "ai-orch"
	}
	cmd := []string{exe, "opencode", "refresh", "--scope", scope}
	if strings.TrimSpace(configPath) != "" {
		cmd = append(cmd, "--path", configPath)
	}
	return cmd
}

func writeOpenCodeRefreshJob(goos string, command []string) (string, error) {
	switch goos {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path := filepath.Join(home, "Library", "LaunchAgents", "com.ai-orch.opencode-refresh.plist")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		return path, os.WriteFile(path, []byte(macOSLaunchAgentPlist("com.ai-orch.opencode-refresh", command)), 0o644)
	case "windows":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path := filepath.Join(home, "ai-orch-opencode-refresh.ps1")
		return path, os.WriteFile(path, []byte(windowsRefreshTaskPowerShell(command)), 0o600)
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path := filepath.Join(home, ".config", "systemd", "user", "ai-orch-opencode-refresh.service")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		return path, os.WriteFile(path, []byte(systemdUserRefreshService(command)), 0o644)
	}
}

func macOSLaunchAgentPlist(label string, command []string) string {
	type plist struct {
		XMLName xml.Name `xml:"plist"`
		Version string   `xml:"version,attr"`
		Comment string   `xml:"dict>string"`
	}
	_ = plist{}
	args := make([]string, 0, len(command))
	for _, arg := range command {
		args = append(args, "    <string>"+xmlEscape(arg)+"</string>")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + xmlEscape(label) + `</string>
  <key>ProgramArguments</key>
  <array>
` + strings.Join(args, "\n") + `
  </array>
  <key>StartInterval</key>
  <integer>86400</integer>
  <key>StandardOutPath</key>
  <string>/tmp/ai-orch-opencode-refresh.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/ai-orch-opencode-refresh.err</string>
  <key>Comment</key>
  <string>Refreshes AI-Orch-routed OpenCode configuration without storing provider keys.</string>
</dict>
</plist>
`
}

func windowsRefreshTaskPowerShell(command []string) string {
	quoted := make([]string, 0, len(command))
	for _, arg := range command {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", "''")+"'")
	}
	cmd := strings.Join(quoted, ", ")
	return "$Action = New-ScheduledTaskAction -Execute " + quotedPowerShell(command[0]) + " -Argument " + quotedPowerShell(strings.Join(command[1:], " ")) + "\n" +
		"$Trigger = New-ScheduledTaskTrigger -Daily -At 9am\n" +
		"# Refreshes AI-Orch-routed OpenCode configuration without storing provider keys.\n" +
		"Register-ScheduledTask -TaskName 'AI-Orch OpenCode Refresh' -Action $Action -Trigger $Trigger -Description 'Refresh AI-Orch-routed OpenCode config' -Force\n" +
		"# Equivalent argv: @(" + cmd + ")\n"
}

func systemdUserRefreshService(command []string) string {
	return "[Unit]\nDescription=Refresh AI-Orch-routed OpenCode configuration\n\n[Service]\nType=oneshot\nExecStart=" + strings.Join(command, " ") + "\n"
}

func quotedPowerShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func xmlEscape(value string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}
