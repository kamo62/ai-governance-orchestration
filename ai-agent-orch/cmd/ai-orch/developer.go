package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"
)

func handleDeveloper(ctx context.Context, cfg Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch developer enroll --client opencode [--scope global|project]")
		os.Exit(1)
	}
	switch args[0] {
	case "enroll":
		if err := developerEnroll(ctx, cfg, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "developer enrolment failed: %v\n", err)
			os.Exit(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown developer command: %s\n", args[0])
		os.Exit(1)
	}
}

func developerEnroll(ctx context.Context, cfg Config, args []string) error {
	fs := flag.NewFlagSet("developer enroll", flag.ContinueOnError)
	client := fs.String("client", "opencode", "developer client to configure")
	scope := fs.String("scope", "global", "OpenCode config scope: global or project")
	configPath := fs.String("path", "", "explicit OpenCode config path")
	classification := fs.String("classification", "internal", "classification header for AI-Orch-routed OpenCode")
	installJob := fs.Bool("install-refresh-job", true, "install a user-level AI-Orch-routed OpenCode refresh job")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *client != "opencode" {
		return fmt.Errorf("unsupported client %q; beta supports opencode", *client)
	}
	if _, err := doGet(ctx, cfg, cfg.GovernanceURL+"/v1/copilot/models"); err != nil {
		fmt.Fprintln(os.Stderr, "Copilot enrolment needs refresh; starting GitHub device login")
		copilotRemoteLogin(ctx, cfg)
	}
	cred, err := requestDeveloperRuntimeCredential(ctx, cfg, "opencode")
	if err != nil {
		return err
	}
	installArgs := []string{"--scope", *scope, "--force", "--runtime-token", cred.RuntimeToken, "--actor-subject", cred.ActorSubject, "--classification", *classification}
	if *configPath != "" {
		installArgs = append(installArgs, "--path", *configPath)
	}
	if err := installOpenCodeConfig(cfg.ModelGatewayURL, installArgs); err != nil {
		return err
	}
	if *installJob {
		path, err := writeOpenCodeRefreshJob(runtime.GOOS, defaultOpenCodeRefreshCommand(*scope, *configPath))
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not install refresh job: %v\n", err)
		} else if path != "" {
			fmt.Fprintf(os.Stderr, "AI-Orch-routed OpenCode refresh job installed: %s\n", path)
		}
	}
	fmt.Printf("AI-Orch-routed OpenCode enrolled for %s; runtime credential expires %s\n", cred.ActorSubject, cred.ExpiresAt.Format(time.RFC3339))
	return nil
}

type developerRuntimeCredentialResponse struct {
	ActorSubject  string    `json:"actor_subject"`
	RuntimeToken  string    `json:"runtime_token"`
	CredentialID  string    `json:"credential_id"`
	ExpiresAt     time.Time `json:"expires_at"`
	ExpiresInDays int       `json:"expires_in_days"`
}

func requestDeveloperRuntimeCredential(ctx context.Context, cfg Config, client string) (developerRuntimeCredentialResponse, error) {
	host, _ := os.Hostname()
	body, _ := json.Marshal(map[string]string{"client": client, "device_name": host})
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/developer/runtime-credential", body)
	if err != nil {
		return developerRuntimeCredentialResponse{}, err
	}
	var parsed developerRuntimeCredentialResponse
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		return developerRuntimeCredentialResponse{}, fmt.Errorf("parse runtime credential response: %w", err)
	}
	if parsed.RuntimeToken == "" || parsed.ActorSubject == "" {
		return developerRuntimeCredentialResponse{}, fmt.Errorf("runtime credential response missing token or actor: %s", resp)
	}
	return parsed, nil
}
