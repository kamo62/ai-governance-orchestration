package server

import (
	"encoding/json"
	"net/http"
	"time"
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
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"service": service,
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := ready(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":  "unavailable",
				"service": service,
				"error":   err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "ready",
			"service": service,
		})
	})

	mux.HandleFunc("/v1/catalog/summary", func(w http.ResponseWriter, r *http.Request) {
		if summary == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "catalog summary unavailable"})
			return
		}
		result, err := summary()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
