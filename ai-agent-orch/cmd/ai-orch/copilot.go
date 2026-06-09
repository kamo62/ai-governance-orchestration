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

func handleCopilot(ctx context.Context, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-orch copilot login|status|models|logout|smoke")
		os.Exit(1)
	}
	switch args[0] {
	case "login":
		copilotLogin(ctx)
	case "status":
		copilotStatus(ctx)
	case "models":
		copilotModels(ctx)
	case "logout":
		copilotLogout(ctx)
	case "smoke":
		copilotSmoke(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown copilot subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

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
		ActorSubject: copilotActorSubject(),
		GitHubLogin:  user.Login,
		GitHubUserID: fmt.Sprintf("%d", user.ID),
		BaseURL:      copilot.DefaultCopilotBaseURL,
		AccessToken:  token.AccessToken,
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
	body, err := copilot.NewClient().Models(ctx, rec.AccessToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copilot models: %v\n", err)
		os.Exit(2)
	}
	prettyPrintJSON(string(body))
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
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": 32,
	})
	resp, err := copilot.NewClient().ChatCompletion(ctx, rec.AccessToken, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "copilot smoke: %v\n", err)
		os.Exit(2)
	}
	prettyPrintJSON(string(resp))
}
