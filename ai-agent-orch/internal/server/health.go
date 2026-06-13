package server

import (
	"net/http"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/httpx"
)

type CatalogSummary struct {
	Agents int `json:"agents"`
	Models int `json:"models"`
}

type ReadinessFunc func() error

type CatalogSummaryFunc func() (CatalogSummary, error)

func New(service string, ready ReadinessFunc, summary CatalogSummaryFunc) http.Handler {
	mux := http.NewServeMux()
	if ready == nil {
		ready = func() error { return nil }
	}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"service": service,
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := ready(); err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":  "unavailable",
				"service": service,
				"error":   err.Error(),
			})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "ready",
			"service": service,
		})
	})

	mux.HandleFunc("/v1/catalog/summary", func(w http.ResponseWriter, r *http.Request) {
		if summary == nil {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "catalog summary unavailable"})
			return
		}
		result, err := summary()
		if err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, result)
	})

	return mux
}
