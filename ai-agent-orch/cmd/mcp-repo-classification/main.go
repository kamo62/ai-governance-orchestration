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
	mux.Handle("/getRepoClassification", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RepoURL string `json:"repo_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"repo_url":       req.RepoURL,
			"classification": "internal",
			"source":         "local-default",
			"timestamp":      "2026-05-23T00:00:00Z",
		})
	})))
	mux.Handle("/getRepoOwner", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"segment": "local",
			"owner":   "local-dev",
		})
	})))
	mux.Handle("/listClassifiedRepos", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"repos": []string{"ai-agent-orch"},
		})
	})))

	addr := ":8091"
	log.Printf("repo-classification MCP listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
