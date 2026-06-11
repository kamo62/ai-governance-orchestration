package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"ai-agent-orch/internal/copilot"
)

// handleCopilot enrolls and inspects per-user Copilot credentials. By default
// it talks to the running Governance Shell so the token lands in the store the
// model gateway reads, keyed by the same actor subject sessions use. The
// --local flag operates on the local token database instead, for setups where
// the shell runs natively on this machine with shared environment config.
func handleCopilot(ctx context.Context, cfg Config, args []string) {
	local := hasFlag(args, "--local")
	args = withoutFlag(args, "--local")
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch copilot login|status|models|logout|refresh|smoke [--local]")
		os.Exit(1)
	}
	if local {
		handleCopilotLocal(ctx, args)
		return
	}
	switch args[0] {
	case "login":
		copilotRemoteLogin(ctx, cfg)
	case "status":
		copilotRemoteGet(ctx, cfg, "/v1/copilot/status")
	case "models":
		copilotRemoteGet(ctx, cfg, "/v1/copilot/models")
	case "logout":
		copilotRemoteLogout(ctx, cfg)
	case "refresh":
		copilotRemoteRefresh(ctx, cfg)
	case "smoke":
		fmt.Fprintln(os.Stderr, "copilot smoke uses the local token store; run: ai-orch copilot smoke --local")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown copilot subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func handleCopilotLocal(ctx context.Context, args []string) {
	switch args[0] {
	case "login":
		copilotLogin(ctx)
	case "status":
		copilotStatus(ctx)
	case "models":
		copilotModels(ctx)
	case "logout":
		copilotLogout(ctx)
	case "refresh":
		copilotLocalRefresh(ctx)
	case "smoke":
		copilotSmoke(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown copilot subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func withoutFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != name {
			out = append(out, a)
		}
	}
	return out
}

// Remote mode: enrollment through the Governance Shell API.

func copilotRemoteLogin(ctx context.Context, cfg Config) {
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/copilot/login/start", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start copilot login: %v\n", err)
		os.Exit(2)
	}
	var start struct {
		LoginID                 string `json:"login_id"`
		ActorSubject            string `json:"actor_subject"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal([]byte(resp), &start); err != nil || start.LoginID == "" {
		fmt.Fprintf(os.Stderr, "unexpected login response: %s\n", resp)
		os.Exit(2)
	}
	url := start.VerificationURIComplete
	if url == "" {
		url = start.VerificationURI
	}
	fmt.Printf("Enrolling Copilot for actor %s\n", start.ActorSubject)
	fmt.Printf("Open %s\n", url)
	fmt.Printf("Code: %s\n", start.UserCode)

	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(start.ExpiresIn+60) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "login cancelled")
			os.Exit(2)
		case <-time.After(interval):
		}
		statusResp, err := doGet(ctx, cfg, cfg.GovernanceURL+"/v1/copilot/login/"+start.LoginID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "poll copilot login: %v\n", err)
			os.Exit(2)
		}
		var status struct {
			Done        bool   `json:"done"`
			Error       string `json:"error"`
			GitHubLogin string `json:"github_login"`
		}
		_ = json.Unmarshal([]byte(statusResp), &status)
		if status.Error != "" {
			fmt.Fprintf(os.Stderr, "copilot login failed: %s\n", status.Error)
			os.Exit(2)
		}
		if status.Done {
			fmt.Printf("Logged in as %s for actor %s\n", status.GitHubLogin, start.ActorSubject)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "copilot login timed out before authorization completed")
	os.Exit(2)
}

func copilotRemoteGet(ctx context.Context, cfg Config, path string) {
	resp, err := doGet(ctx, cfg, cfg.GovernanceURL+path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copilot request failed: %v\n", err)
		os.Exit(2)
	}
	prettyPrintJSON(resp)
}

func copilotRemoteLogout(ctx context.Context, cfg Config) {
	resp, err := doPost(ctx, cfg, cfg.GovernanceURL+"/v1/copilot/logout", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copilot logout failed: %v\n", err)
		os.Exit(2)
	}
	prettyPrintJSON(resp)
}

// copilotRemoteRefresh proves the stored credential still reaches Copilot.
// The gateway exchanges short-lived session tokens automatically, so a
// successful model listing means no re-enrollment is needed.
func copilotRemoteRefresh(ctx context.Context, cfg Config) {
	if _, err := doGet(ctx, cfg, cfg.GovernanceURL+"/v1/copilot/models"); err != nil {
		fmt.Fprintf(os.Stderr, "copilot credential check failed: %v\nRe-enroll with: ai-orch copilot login\n", err)
		os.Exit(2)
	}
	fmt.Println("Copilot credential verified; session tokens refresh automatically on use")
}

// Local mode: direct token store on this machine.

func copilotActorSubject() string {
	if v := strings.TrimSpace(os.Getenv("AI_ORCH_ACTOR_SUBJECT")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("USER")); v != "" {
		return v
	}
	return "local-dev"
}

func openCopilotStoreOrExit() *copilot.Store {
	store, err := copilot.OpenStore(os.Getenv("AI_ORCH_COPILOT_TOKEN_DB"), os.Getenv("AI_ORCH_COPILOT_TOKEN_ENCRYPTION_KEY"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "copilot token store: %v\n", err)
		os.Exit(2)
	}
	return store
}

func copilotLogin(ctx context.Context) {
	store := openCopilotStoreOrExit()
	defer store.Close()
	client := copilot.NewClient()
	device, err := client.StartDeviceFlow(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start device flow: %v\n", err)
		os.Exit(2)
	}
	url := device.VerificationURIComplete
	if url == "" {
		url = device.VerificationURI
	}
	fmt.Printf("Enrolling Copilot for actor %s\n", copilotActorSubject())
	fmt.Printf("Open %s\n", url)
	fmt.Printf("Code: %s\n", device.UserCode)
	loginCtx, cancel := context.WithTimeout(ctx, time.Duration(device.ExpiresIn+60)*time.Second)
	defer cancel()
	token, err := client.PollAccessToken(loginCtx, device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "poll device flow: %v\n", err)
		os.Exit(2)
	}
	user, err := client.User(ctx, token.AccessToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "github user lookup: %v\n", err)
		os.Exit(2)
	}
	if err := store.Save(ctx, copilot.TokenRecord{
		ActorSubject:     copilotActorSubject(),
		GitHubLogin:      user.Login,
		GitHubUserID:     fmt.Sprintf("%d", user.ID),
		BaseURL:          copilot.DefaultCopilotBaseURL,
		AccessToken:      token.AccessToken,
		RefreshToken:     token.RefreshToken,
		AccessExpiresAt:  token.AccessExpiresAt(time.Now().UTC()),
		RefreshExpiresAt: token.RefreshExpiresAt(time.Now().UTC()),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "save token: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("Logged in as %s for actor %s\n", user.Login, copilotActorSubject())
}

func copilotStatus(ctx context.Context) {
	store := openCopilotStoreOrExit()
	defer store.Close()
	rec, err := store.Load(ctx, copilotActorSubject())
	if err != nil {
		fmt.Printf("Copilot: not configured for actor %s (%v)\n", copilotActorSubject(), err)
		return
	}
	fmt.Printf("Copilot: configured\nActor: %s\nGitHub: %s\nToken: %s\n", rec.ActorSubject, rec.GitHubLogin, rec.Fingerprint)
}

func copilotModels(ctx context.Context) {
	store := openCopilotStoreOrExit()
	defer store.Close()
	rec, err := store.Load(ctx, copilotActorSubject())
	if err != nil {
		fmt.Fprintf(os.Stderr, "load copilot token: %v\n", err)
		os.Exit(2)
	}
	rec = refreshLocalCopilotToken(ctx, store, rec)
	body, err := copilot.NewClient().Models(ctx, copilotBearer(ctx, rec))
	if err != nil {
		fmt.Fprintf(os.Stderr, "copilot models: %v\n", err)
		os.Exit(2)
	}
	prettyPrintJSON(string(body))
}

func copilotLocalRefresh(ctx context.Context) {
	store := openCopilotStoreOrExit()
	defer store.Close()
	rec, err := store.Load(ctx, copilotActorSubject())
	if err != nil {
		fmt.Fprintf(os.Stderr, "load copilot token: %v\n", err)
		os.Exit(2)
	}
	rec = refreshLocalCopilotToken(ctx, store, rec)
	client := copilot.NewClient()
	session, err := client.ExchangeSessionToken(ctx, rec.AccessToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copilot session token exchange failed: %v\nRe-enroll with: ai-orch copilot login --local\n", err)
		os.Exit(2)
	}
	if session.ExpiresAt.IsZero() {
		fmt.Println("Copilot session token issued (no expiry reported)")
		return
	}
	fmt.Printf("Copilot session token issued, expires %s\n", session.ExpiresAt.Format(time.RFC3339))
}

func copilotLogout(ctx context.Context) {
	store := openCopilotStoreOrExit()
	defer store.Close()
	if err := store.Delete(ctx, copilotActorSubject()); err != nil {
		fmt.Fprintf(os.Stderr, "delete token: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("Copilot token removed for actor %s\n", copilotActorSubject())
}

func copilotSmoke(ctx context.Context, args []string) {
	model := "gpt-5-mini"
	prompt := "Reply exactly: copilot-smoke-ok"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				i++
				model = args[i]
			}
		case "--prompt":
			if i+1 < len(args) {
				i++
				prompt = args[i]
			}
		}
	}
	store := openCopilotStoreOrExit()
	defer store.Close()
	rec, err := store.Load(ctx, copilotActorSubject())
	if err != nil {
		fmt.Fprintf(os.Stderr, "load copilot token: %v\n", err)
		os.Exit(2)
	}
	rec = refreshLocalCopilotToken(ctx, store, rec)
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": 32,
	})
	resp, err := copilot.NewClient().ChatCompletion(ctx, copilotBearer(ctx, rec), body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copilot smoke: %v\n", err)
		os.Exit(2)
	}
	prettyPrintJSON(string(resp))
}

func refreshLocalCopilotToken(ctx context.Context, store *copilot.Store, rec copilot.TokenRecord) copilot.TokenRecord {
	if store == nil || rec.RefreshToken == "" || rec.AccessExpiresAt.IsZero() || time.Now().UTC().Before(rec.AccessExpiresAt.Add(-5*time.Minute)) {
		return rec
	}
	refreshed, err := copilot.NewClient().RefreshAccessToken(ctx, rec.RefreshToken)
	if err != nil {
		return rec
	}
	updated, err := store.UpdateOAuthToken(ctx, rec.ActorSubject, refreshed, time.Now().UTC())
	if err != nil {
		return rec
	}
	return updated
}

// copilotBearer exchanges the OAuth token for a short-lived Copilot bearer,
// falling back to the OAuth token when the exchange endpoint is unavailable.
func copilotBearer(ctx context.Context, rec copilot.TokenRecord) string {
	session, err := copilot.NewClient().ExchangeSessionToken(ctx, rec.AccessToken)
	if err != nil {
		return rec.AccessToken
	}
	return session.Token
}
