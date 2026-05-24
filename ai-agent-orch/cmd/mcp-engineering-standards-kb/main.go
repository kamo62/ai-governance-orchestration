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
	mux.Handle("/getStandard", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"name":         "Local Engineering Standards",
			"version":      "1.0",
			"content":      "Use Playwright for browser tests. Use Go for backend services.",
			"last_updated": "2026-05-23",
		})
	})))
	mux.Handle("/listStandards", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"standards": []string{"testing", "security", "documentation"},
		})
	})))
	mux.Handle("/searchStandards", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"results": []string{"Playwright test standard v1.0"},
		})
	})))

	addr := ":8092"
	log.Printf("engineering-standards-kb MCP listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
