package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/governance"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/logx"
)

func waitForModelBackendHealth(ctx context.Context, backend modelBackendHealth, interval time.Duration) error {
	if backend == nil {
		return nil
	}
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	var lastErr error
	for {
		if err := backend.Health(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("backend did not become healthy before timeout: %w; last error: %v", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

// logRequestLatency wraps an http.Handler, tags every request with an ID, and
// logs requests that exceed a threshold. The ID is echoed in the X-Request-ID
// response header so a slow or failing call can be joined to client logs.
func logRequestLatency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		start := time.Now()
		next.ServeHTTP(w, r)
		dur := time.Since(start)
		if dur > 500*time.Millisecond {
			logx.Warnf("slow request: %s %s %s request_id=%s", r.Method, r.URL.Path, dur, requestID)
		}
	})
}

func newRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return "req_" + hex.EncodeToString(b[:])
}

func containsSuffix(path, suffix string) bool {
	return len(path) > len(suffix) && path[len(path)-len(suffix):] == suffix
}

func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		logx.Warnf("invalid %s=%q; using %s", key, value, fallback)
		return fallback
	}
	return duration
}

func bootstrapModelPricing(ctx context.Context, store governance.ModelPricingStore, fetcher governance.OpenRouterPricingFetcher) bool {
	if store == nil {
		return false
	}
	timeout := envDurationOrDefault("AI_ORCH_MODEL_PRICING_BOOTSTRAP_TIMEOUT", 15*time.Second)
	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	count, err := governance.RefreshModelPricing(refreshCtx, store, fetcher)
	if err != nil {
		logx.Warnf("model pricing bootstrap failed: %v", err)
		return false
	}
	logx.Infof("model pricing bootstrapped: %d models", count)
	return true
}
