package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamEventsFromURLReturnsPatchIDsAndRuntimeErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer local-test-token" {
			t.Fatalf("expected auth header, got %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"model_usage","payload":"{\"model\":\"openai/gpt-5.5\",\"reasoning_effort\":\"high\"}"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"patch","payload":"{\"patchId\":\"patch_1\",\"files\":[]}"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"error","payload":"dispatch failed"}`+"\n\n")
	}))
	defer server.Close()

	result, err := streamEventsFromURL(context.Background(), Config{Token: "local-test-token"}, server.URL)
	if err == nil {
		t.Fatal("expected error event to fail the stream result")
	}
	if result.Count != 3 {
		t.Fatalf("expected 3 events, got %d", result.Count)
	}
	if len(result.PatchIDs) != 1 || result.PatchIDs[0] != "patch_1" {
		t.Fatalf("expected patch_1, got %#v", result.PatchIDs)
	}
	if len(result.ModelUsage) != 1 || !strings.Contains(result.ModelUsage[0], "gpt-5.5") {
		t.Fatalf("expected model usage with gpt-5.5, got %#v", result.ModelUsage)
	}
}

func TestStreamEventsFromURLReturnsPatchesWithoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"patch","payload":"{\"patch_id\":\"patch_2\",\"files\":[]}"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"done","payload":"ok"}`+"\n\n")
	}))
	defer server.Close()

	result, err := streamEventsFromURL(context.Background(), Config{Token: "local-test-token"}, server.URL)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result.Count != 2 {
		t.Fatalf("expected 2 events, got %d", result.Count)
	}
	if len(result.PatchIDs) != 1 || result.PatchIDs[0] != "patch_2" {
		t.Fatalf("expected patch_2, got %#v", result.PatchIDs)
	}
}

func TestKillSwitchToggleRequestUsesPostToEnableAndDeleteToDisable(t *testing.T) {
	cfg := Config{GovernanceURL: "http://governance"}

	method, url := killSwitchToggleRequest(cfg, "agent", "unit-tests", true)
	if method != http.MethodPost {
		t.Fatalf("enable method = %s, want POST", method)
	}
	if url != "http://governance/v1/admin/killswitch/agent/unit-tests" {
		t.Fatalf("unexpected enable url %q", url)
	}

	method, url = killSwitchToggleRequest(cfg, "agent", "unit-tests", false)
	if method != http.MethodDelete {
		t.Fatalf("disable method = %s, want DELETE", method)
	}
	if url != "http://governance/v1/admin/killswitch/agent/unit-tests" {
		t.Fatalf("unexpected disable url %q", url)
	}
}

func TestDoRequestUsesAdminTokenForAdminRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/killswitch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer local-admin-token" {
			t.Fatalf("expected admin auth header, got %q", got)
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	cfg := Config{
		GovernanceURL: server.URL,
		Token:         "local-dev-token",
		AdminToken:    "local-admin-token",
	}

	if _, err := doGet(context.Background(), cfg, server.URL+"/v1/admin/killswitch"); err != nil {
		t.Fatalf("admin request failed: %v", err)
	}
}

func TestIsAdminRouteHandlesBasePathProxies(t *testing.T) {
	if !isAdminRoute("https://example.test/governance/v1/admin/killswitch") {
		t.Fatal("expected base-path admin route to be detected")
	}
	if isAdminRoute("https://example.test/governance/v1/agents") {
		t.Fatal("did not expect user route to be treated as admin")
	}
}

func TestDoRequestUsesDeveloperTokenForUserRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agents" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer local-dev-token" {
			t.Fatalf("expected developer auth header, got %q", got)
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()

	cfg := Config{
		GovernanceURL: server.URL,
		Token:         "local-dev-token",
		AdminToken:    "local-admin-token",
	}

	if _, err := doGet(context.Background(), cfg, server.URL+"/v1/agents"); err != nil {
		t.Fatalf("developer request failed: %v", err)
	}
}

func TestIsHelpOnly(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{name: "long help", args: []string{"--help"}, want: true},
		{name: "short help", args: []string{"-h"}, want: true},
		{name: "prompt value", args: []string{"--prompt", "--help"}, want: false},
		{name: "empty", args: nil, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHelpOnly(tc.args); got != tc.want {
				t.Fatalf("isHelpOnly(%#v) = %t, want %t", tc.args, got, tc.want)
			}
		})
	}
}

func TestRuntimeDoctorChecksHealthySetup(t *testing.T) {
	governance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Fatalf("unexpected governance path %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"service":"governance-shell","status":"ready"}`)
	}))
	defer governance.Close()
	modelGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected model gateway path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer runtime-token" {
			t.Fatalf("expected runtime bearer token, got %q", got)
		}
		fmt.Fprint(w, `{"object":"list","data":[]}`)
	}))
	defer modelGateway.Close()

	checks := runtimeDoctorChecks(context.Background(), Config{
		GovernanceURL:   governance.URL,
		ModelGatewayURL: modelGateway.URL,
		Token:           "dev-token",
		RuntimeToken:    "runtime-token",
	}, governance.Client())

	want := []string{
		"Developer token: OK",
		"Runtime token: OK",
		"Governance Shell readyz: OK",
		"Model gateway: OK",
	}
	for _, item := range want {
		if !containsString(checks, item) {
			t.Fatalf("expected %q in checks, got %#v", item, checks)
		}
	}
}

func TestRuntimeDoctorChecksMissingTokensAndBrokenServices(t *testing.T) {
	governance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer governance.Close()

	checks := runtimeDoctorChecks(context.Background(), Config{
		GovernanceURL:   governance.URL,
		ModelGatewayURL: "http://127.0.0.1:1",
	}, governance.Client())

	wantSubstrings := []string{
		"Developer token: MISSING",
		"Runtime token: MISSING",
		"Governance Shell readyz: FAIL",
		"Model gateway: SKIP",
	}
	for _, item := range wantSubstrings {
		if !containsSubstring(checks, item) {
			t.Fatalf("expected substring %q in checks, got %#v", item, checks)
		}
	}
}

func TestParseOpenCodeWrapperArgs(t *testing.T) {
	opts := parseOpenCodeWrapperArgs([]string{
		"--governance-agent", "code-review",
		"--governance-classification", "internal",
		"--governance-prompt", "review safely",
		"--", "run", "--model", "ai-orch/copilot-gpt-5-mini", "hello",
	})
	if opts.GovernanceAgent != "code-review" || opts.Classification != "internal" || opts.SessionPrompt != "review safely" {
		t.Fatalf("unexpected governance args: %#v", opts)
	}
	want := []string{"run", "--model", "ai-orch/copilot-gpt-5-mini", "hello"}
	if len(opts.OpenCodeArgs) != len(want) {
		t.Fatalf("unexpected opencode args: %#v", opts.OpenCodeArgs)
	}
	for i := range want {
		if opts.OpenCodeArgs[i] != want[i] {
			t.Fatalf("unexpected opencode args: %#v", opts.OpenCodeArgs)
		}
	}
}

func TestParseOpenCodeWrapperArgsDefaultsToGovernanceLead(t *testing.T) {
	opts := parseOpenCodeWrapperArgs([]string{"--", "run", "hello"})
	if opts.GovernanceAgent != "governance-lead" {
		t.Fatalf("expected governance-lead default, got %q", opts.GovernanceAgent)
	}
	if opts.Classification != "internal" {
		t.Fatalf("expected internal default, got %q", opts.Classification)
	}
	if opts.ModelOnly {
		t.Fatal("did not expect model-only mode by default")
	}
}

func TestParseOpenCodeWrapperArgsModelOnlyRequiresIntent(t *testing.T) {
	opts := parseOpenCodeWrapperArgs([]string{
		"--model-only",
		"--governance-intent", "Need direct model exploration before choosing a specialist",
		"--", "run", "hello",
	})
	if !opts.ModelOnly {
		t.Fatal("expected model-only mode")
	}
	if opts.GovernanceAgent != "model-gateway" {
		t.Fatalf("expected model-gateway agent, got %q", opts.GovernanceAgent)
	}
	if opts.Intent == "" {
		t.Fatal("expected intent reason")
	}

	if err := validateOpenCodeWrapperOptions(opts); err != nil {
		t.Fatalf("expected valid model-only options: %v", err)
	}

	missingReason := parseOpenCodeWrapperArgs([]string{"--model-only", "--", "run", "hello"})
	if err := validateOpenCodeWrapperOptions(missingReason); err == nil {
		t.Fatal("expected model-only mode to require a governance intent reason")
	}
}

func TestCreateOpenCodeGovernedSessionUsesLeadRunReadOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs" {
			t.Fatalf("expected /v1/runs, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer dev-token" {
			t.Fatalf("expected dev token auth, got %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["agent"] != "governance-lead" {
			t.Fatalf("expected governance-lead, got %#v", body["agent"])
		}
		if body["permission_mode"] != "read_only" {
			t.Fatalf("expected read_only lead permission, got %#v", body["permission_mode"])
		}
		if body["approval_mode"] != "manual" {
			t.Fatalf("expected manual approval, got %#v", body["approval_mode"])
		}
		fmt.Fprint(w, `{"session_id":"sess_1","gateway_token":"sgt_1","specialist":"code-review"}`)
	}))
	defer server.Close()

	session, err := createOpenCodeGovernedSession(context.Background(), Config{
		GovernanceURL: server.URL,
		Token:         "dev-token",
	}, openCodeWrapperOptions{
		GovernanceAgent: defaultOpenCodeLeadAgent,
		Classification:  "internal",
		SessionPrompt:   "route this",
	})
	if err != nil {
		t.Fatalf("create governed session: %v", err)
	}
	if session.SessionID != "sess_1" || session.GatewayToken != "sgt_1" || session.Specialist != "code-review" {
		t.Fatalf("unexpected session tokens: %#v", session)
	}
}

func TestOpenCodeModelHelpersInjectDefaultOnlyWhenMissing(t *testing.T) {
	args := withDefaultOpenCodeModel([]string{"run", "hello"}, "ai-orch/coding-gpt55")
	want := []string{"run", "--model", "ai-orch/coding-gpt55", "hello"}
	if len(args) != len(want) {
		t.Fatalf("unexpected args: %#v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("unexpected args: %#v", args)
		}
	}

	explicit := withDefaultOpenCodeModel([]string{"run", "--model", "ai-orch/opus-4.8", "hello"}, "ai-orch/coding-gpt55")
	if explicit[2] != "ai-orch/opus-4.8" {
		t.Fatalf("explicit model should be preserved: %#v", explicit)
	}
}

func TestOpenCodeAgentHelperInjectsGovernanceLeadOnlyWhenMissing(t *testing.T) {
	args := withDefaultOpenCodeAgent([]string{"run", "hello"}, "governance-lead")
	want := []string{"run", "--agent", "governance-lead", "hello"}
	if len(args) != len(want) {
		t.Fatalf("unexpected args: %#v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("unexpected args: %#v", args)
		}
	}

	explicit := withDefaultOpenCodeAgent([]string{"run", "--agent", "build", "hello"}, "governance-lead")
	if explicit[2] != "build" {
		t.Fatalf("explicit agent should be preserved: %#v", explicit)
	}
}

func TestOpenCodeLeadModelUsesCapabilityAlias(t *testing.T) {
	got := defaultOpenCodeLeadModel(context.Background(), Config{}, nil)
	if got != "ai-orch/coding-gpt55" {
		t.Fatalf("expected coding capability alias, got %q", got)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsSubstring(items []string, want string) bool {
	for _, item := range items {
		if strings.Contains(item, want) {
			return true
		}
	}
	return false
}
