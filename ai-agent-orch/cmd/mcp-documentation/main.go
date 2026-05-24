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
	mux.Handle("/getPage", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"title":   req.Title,
			"content": "This is a mock documentation page for " + req.Title + ".",
			"last_updated": "2026-05-24T00:00:00Z",
			"author":  "local-dev",
		})
	})))
	mux.Handle("/searchPages", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"query": req.Query,
			"pages": []map[string]any{
				{"title": "Architecture Overview", "snippet": "System boundaries and data flow..."},
				{"title": "Deployment Guide", "snippet": "Docker Compose setup and health checks..."},
			},
		})
	})))
	mux.Handle("/listSections", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"sections": []string{"Getting Started", "Architecture", "Governance", "Runtime", "Operations"},
		})
	})))

	addr := ":8096"
	log.Printf("documentation MCP listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
