package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"ai-agent-orch/internal/httpauth"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	mcpToken := os.Getenv("AI_ORCH_MCP_TOKEN")
	mux.Handle("/getIssues", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Repo string `json:"repo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		issues := []map[string]any{
			{"id": 1, "title": "Add retry logic to OpenRouter client", "state": "open", "labels": []string{"enhancement"}},
			{"id": 2, "title": "Document kill switch behavior", "state": "open", "labels": []string{"docs"}},
			{"id": 3, "title": "Fix classification routing edge case", "state": "closed", "labels": []string{"bug"}},
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"repo":   req.Repo,
			"issues": issues,
		})
	})))
	mux.Handle("/getPullRequests", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Repo string `json:"repo"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"repo": req.Repo,
			"pull_requests": []map[string]any{
				{"id": 10, "title": "Phase 1 local vertical slice", "state": "merged", "author": "local-contributor"},
				{"id": 11, "title": "Add SQLite audit backend", "state": "open", "author": "local-contributor"},
			},
		})
	})))
	mux.Handle("/getIssueComments", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IssueID int `json:"issue_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"issue_id": req.IssueID,
			"comments": []map[string]any{
				{"author": "reviewer", "body": "LGTM after addressing the auth fallback concern."},
			},
		})
	})))

	addr := ":8095"
	log.Printf("issue-tracker MCP listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
