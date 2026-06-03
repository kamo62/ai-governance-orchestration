package main

import (
	"context"
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

	method, url := killSwitchToggleRequest(cfg, "agent", "test-generation", true)
	if method != http.MethodPost {
		t.Fatalf("enable method = %s, want POST", method)
	}
	if url != "http://governance/v1/admin/killswitch/agent/test-generation" {
		t.Fatalf("unexpected enable url %q", url)
	}

	method, url = killSwitchToggleRequest(cfg, "agent", "test-generation", false)
	if method != http.MethodDelete {
		t.Fatalf("disable method = %s, want DELETE", method)
	}
	if url != "http://governance/v1/admin/killswitch/agent/test-generation" {
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
