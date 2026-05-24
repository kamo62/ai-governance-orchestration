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
	mux.Handle("/getTestResults", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Commit string `json:"commit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
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
		})
	})))
	mux.Handle("/getCoverage", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Package string `json:"package"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"package":  req.Package,
			"coverage": "78.5%",
			"lines": map[string]any{
				"total":   1200,
				"covered": 942,
				"missed":  258,
			},
		})
	})))
	mux.Handle("/getFlakyTests", httpauth.RequireBearerToken(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"flaky_tests": []map[string]any{
				{"name": "TestEventStoreConcurrentSubscribe", "flake_rate": "3%"},
			},
		})
	})))

	addr := ":8097"
	log.Printf("test-management MCP listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
