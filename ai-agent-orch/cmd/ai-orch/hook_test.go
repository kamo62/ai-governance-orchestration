package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/hooklane"
)

// hookTestServer answers session-create calls and records request paths.
func hookTestServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sessions" {
			_ = json.NewEncoder(w).Encode(map[string]string{"session_id": "sess_cli"})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func TestRunHookInvalidSubcommandExits2(t *testing.T) {
	var stderr bytes.Buffer
	code := runHook(context.Background(), Config{}, []string{"bogus"}, strings.NewReader("{}"), &stderr, hooklane.Workspace{Dir: t.TempDir()})
	if code != 2 {
		t.Fatalf("expected exit 2 for invalid subcommand, got %d", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("expected usage diagnostic, got %q", stderr.String())
	}
}

func TestRunHookNoArgsExits2(t *testing.T) {
	var stderr bytes.Buffer
	code := runHook(context.Background(), Config{}, nil, strings.NewReader("{}"), &stderr, hooklane.Workspace{Dir: t.TempDir()})
	if code != 2 {
		t.Fatalf("expected exit 2 for missing subcommand, got %d", code)
	}
}

func TestRunHookInvalidStdinExits1(t *testing.T) {
	var stderr bytes.Buffer
	code := runHook(context.Background(), Config{}, []string{"post-tool"}, strings.NewReader("not json"), &stderr, hooklane.Workspace{Dir: t.TempDir()})
	if code != 1 {
		t.Fatalf("expected exit 1 for invalid stdin, got %d", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected a diagnostic on stderr")
	}
}

func TestRunHookRoutesToLifecycle(t *testing.T) {
	cases := []struct {
		sub      string
		stdin    string
		wantPath string
	}{
		{"prompt-submit", `{"prompt":"hi"}`, "POST /v1/sessions"},
		{"post-tool", `{"session_id":"sess_cli","tool_name":"Edit"}`, "POST /v1/evidence"},
		{"stop", `{"session_id":"sess_cli"}`, "POST /v1/evidence"},
	}
	for _, tc := range cases {
		t.Run(tc.sub, func(t *testing.T) {
			srv, paths := hookTestServer(t)
			cfg := Config{GovernanceURL: srv.URL, Token: "dev"}
			var stderr bytes.Buffer
			code := runHook(context.Background(), cfg, []string{tc.sub}, strings.NewReader(tc.stdin), &stderr, hooklane.Workspace{Dir: t.TempDir()})
			if code != 0 {
				t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr.String())
			}
			found := false
			for _, p := range *paths {
				if p == tc.wantPath {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected %s to hit %q, got %v", tc.sub, tc.wantPath, *paths)
			}
		})
	}
}

// TestRunHookSpoolsAndExitsZeroOnServerError verifies a 5xx from the
// Governance Shell during post-tool is queued locally instead of failing the
// hook: the process still exits 0, with a stderr note explaining the event
// was queued for retry.
func TestRunHookSpoolsAndExitsZeroOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	cfg := Config{GovernanceURL: srv.URL, Token: "dev"}
	ws := hooklane.Workspace{Dir: t.TempDir()}
	var stderr bytes.Buffer

	code := runHook(context.Background(), cfg, []string{"post-tool"}, strings.NewReader(`{"session_id":"sess_cli","tool_name":"Edit"}`), &stderr, ws)
	if code != 0 {
		t.Fatalf("expected exit 0 for a spooled event, got %d (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "queued for retry") {
		t.Fatalf("expected a stderr note about the queued event, got %q", stderr.String())
	}
}

// TestRunHookRejects4xxAndExitsOne verifies a 4xx from the Governance Shell
// is still reported as a failure (exit 1) rather than spooled, matching
// pre-spool behavior for permanent rejections.
func TestRunHookRejects4xxAndExitsOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	cfg := Config{GovernanceURL: srv.URL, Token: "dev"}
	ws := hooklane.Workspace{Dir: t.TempDir()}
	var stderr bytes.Buffer

	code := runHook(context.Background(), cfg, []string{"post-tool"}, strings.NewReader(`{"session_id":"sess_cli","tool_name":"Edit"}`), &stderr, ws)
	if code != 1 {
		t.Fatalf("expected exit 1 for a 4xx rejection, got %d (stderr: %s)", code, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected a diagnostic on stderr")
	}
}
