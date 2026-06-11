// mcp-stub serves canned responses for the sample MCP backends used in the
// local demo stack. One binary covers every stub server; pick one by name:
//
//	mcp-stub repo-classification
//	mcp-stub engineering-standards-kb
//	mcp-stub catalog-introspection
//	mcp-stub playwright-cli
//	mcp-stub issue-tracker
//	mcp-stub documentation
//	mcp-stub test-management
//
// These exist so the governed tool path can be exercised end to end without
// real backends. Replace them with registrations that point at real systems.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpauth"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/logx"
)

type stubServer struct {
	addr      string
	endpoints map[string]http.HandlerFunc
}

func main() {
	logx.Setup()
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mcp-stub <%s>\n", strings.Join(stubNames(), "|"))
		os.Exit(1)
	}
	name := os.Args[1]
	server, ok := stubServers()[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown stub server %q; expected one of: %s\n", name, strings.Join(stubNames(), ", "))
		os.Exit(1)
	}

	mcpToken := os.Getenv("AI_ORCH_MCP_TOKEN")
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	for path, handler := range server.endpoints {
		mux.Handle(path, httpauth.RequireBearerToken(mcpToken, handler))
	}

	addr := server.addr
	if override := os.Getenv("AI_ORCH_MCP_STUB_ADDR"); override != "" {
		addr = override
	}
	logx.Infof("%s MCP stub listening on %s", name, addr)
	logx.Fatal(http.ListenAndServe(addr, mux))
}

func stubNames() []string {
	names := make([]string, 0, len(stubServers()))
	for name := range stubServers() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// static returns a handler that always responds with the same payload.
func static(payload map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, payload)
	}
}

// echoing decodes a single request field and builds the response from it.
func echoing[T any](build func(req T) map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req T
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, build(req))
	}
}

func stubServers() map[string]stubServer {
	return map[string]stubServer{
		"repo-classification": {
			addr: ":8091",
			endpoints: map[string]http.HandlerFunc{
				"/getRepoClassification": echoing(func(req struct {
					RepoURL string `json:"repo_url"`
				}) map[string]any {
					return map[string]any{
						"repo_url":       req.RepoURL,
						"classification": "internal",
						"source":         "local-default",
						"timestamp":      "2026-05-23T00:00:00Z",
					}
				}),
				"/getRepoOwner": static(map[string]any{
					"segment": "local",
					"owner":   "local-dev",
				}),
				"/listClassifiedRepos": static(map[string]any{
					"repos": []string{"ai-agent-orch"},
				}),
			},
		},
		"engineering-standards-kb": {
			addr: ":8092",
			endpoints: map[string]http.HandlerFunc{
				"/getStandard": static(map[string]any{
					"name":         "Local Engineering Standards",
					"version":      "1.0",
					"content":      "Use Playwright for browser tests. Use Go for backend services.",
					"last_updated": "2026-05-23",
				}),
				"/listStandards": static(map[string]any{
					"standards": []string{"testing", "security", "documentation"},
				}),
				"/searchStandards": static(map[string]any{
					"results": []string{"Playwright test standard v1.0"},
				}),
			},
		},
		"catalog-introspection": {
			addr: ":8093",
			endpoints: map[string]http.HandlerFunc{
				"/listSpecialists": static(map[string]any{
					"specialists": []string{
						"unit-tests",
						"code-review",
						"documentation",
						"refactor",
						"security-scan",
						"architecture-review",
					},
				}),
				"/getSpecialistMetadata": echoing(func(req struct {
					Name string `json:"name"`
				}) map[string]any {
					return map[string]any{
						"name":        req.Name,
						"phase":       "experimental",
						"description": "Temporary specialist agent.",
					}
				}),
				"/validateAgentRef": static(map[string]any{"valid": true}),
			},
		},
		"playwright-cli": {
			addr: ":8094",
			endpoints: map[string]http.HandlerFunc{
				"/runPlaywrightTest": static(map[string]any{
					"status":   "passed",
					"tests":    3,
					"failures": 0,
					"duration": "1.2s",
				}),
				"/listTestFiles": static(map[string]any{
					"files": []string{"login.spec.ts", "payment.spec.ts"},
				}),
				"/getTestResults": static(map[string]any{
					"status": "passed",
					"output": "All tests passed.",
				}),
			},
		},
		"issue-tracker": {
			addr: ":8095",
			endpoints: map[string]http.HandlerFunc{
				"/getIssues": echoing(func(req struct {
					Repo string `json:"repo"`
				}) map[string]any {
					return map[string]any{
						"repo": req.Repo,
						"issues": []map[string]any{
							{"id": 1, "title": "Add retry logic to OpenRouter client", "state": "open", "labels": []string{"enhancement"}},
							{"id": 2, "title": "Document kill switch behavior", "state": "open", "labels": []string{"docs"}},
							{"id": 3, "title": "Fix classification routing edge case", "state": "closed", "labels": []string{"bug"}},
						},
					}
				}),
				"/getPullRequests": echoing(func(req struct {
					Repo string `json:"repo"`
				}) map[string]any {
					return map[string]any{
						"repo": req.Repo,
						"pull_requests": []map[string]any{
							{"id": 10, "title": "Phase 1 local vertical slice", "state": "merged", "author": "local-contributor"},
							{"id": 11, "title": "Add SQLite audit backend", "state": "open", "author": "local-contributor"},
						},
					}
				}),
				"/getIssueComments": echoing(func(req struct {
					IssueID int `json:"issue_id"`
				}) map[string]any {
					return map[string]any{
						"issue_id": req.IssueID,
						"comments": []map[string]any{
							{"author": "reviewer", "body": "LGTM after addressing the auth fallback concern."},
						},
					}
				}),
			},
		},
		"documentation": {
			addr: ":8096",
			endpoints: map[string]http.HandlerFunc{
				"/getPage": echoing(func(req struct {
					Title string `json:"title"`
				}) map[string]any {
					return map[string]any{
						"title":        req.Title,
						"content":      "This is a mock documentation page for " + req.Title + ".",
						"last_updated": "2026-05-24T00:00:00Z",
						"author":       "local-dev",
					}
				}),
				"/searchPages": echoing(func(req struct {
					Query string `json:"query"`
				}) map[string]any {
					return map[string]any{
						"query": req.Query,
						"pages": []map[string]any{
							{"title": "Architecture Overview", "snippet": "System boundaries and data flow..."},
							{"title": "Deployment Guide", "snippet": "Docker Compose setup and health checks..."},
						},
					}
				}),
				"/listSections": static(map[string]any{
					"sections": []string{"Getting Started", "Architecture", "Governance", "Runtime", "Operations"},
				}),
			},
		},
		"test-management": {
			addr: ":8097",
			endpoints: map[string]http.HandlerFunc{
				"/getTestResults": echoing(func(req struct {
					Commit string `json:"commit"`
				}) map[string]any {
					return map[string]any{
						"commit": req.Commit,
						"summary": map[string]any{
							"total":   42,
							"passed":  40,
							"failed":  2,
							"skipped": 0,
						},
						"failed_tests": []map[string]any{
							{"name": "TestCostCapBlocksExcessive", "error": "assertion failed: expected 402"},
							{"name": "TestSecretScanFalsePositive", "error": "tolerance too strict"},
						},
					}
				}),
				"/getCoverage": echoing(func(req struct {
					Package string `json:"package"`
				}) map[string]any {
					return map[string]any{
						"package":  req.Package,
						"coverage": "78.5%",
						"lines": map[string]any{
							"total":   1200,
							"covered": 942,
							"missed":  258,
						},
					}
				}),
				"/getFlakyTests": static(map[string]any{
					"flaky_tests": []map[string]any{
						{"name": "TestEventStoreConcurrentSubscribe", "flake_rate": "3%"},
					},
				}),
			},
		},
	}
}
