package governance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenRouterPricingFetcherParsesPerTokenCosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"x-ai/grok-build-0.1","pricing":{"prompt":"0.0000004","completion":"0.0000012"}},{"id":"openai/gpt-test","pricing":{"prompt":0.0000005,"completion":0.0000015}}]}`))
	}))
	defer server.Close()

	records, err := (OpenRouterPricingFetcher{BaseURL: server.URL}).Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch pricing: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected two pricing records, got %d", len(records))
	}
	if records[0].Provider != "openrouter" || records[0].ModelID != "x-ai/grok-build-0.1" {
		t.Fatalf("unexpected first record identity: %#v", records[0])
	}
	if records[0].PromptCostPerToken != 0.0000004 || records[0].CompletionCostPerToken != 0.0000012 {
		t.Fatalf("unexpected parsed costs: %#v", records[0])
	}
}

func TestRefreshModelPricingCreatesTableRecordsOnFreshStore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"x-ai/grok-build-0.1","pricing":{"prompt":"0.0000004","completion":"0.0000012"}}]}`))
	}))
	defer server.Close()

	store, err := NewSQLiteModelPricingStore(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("new pricing store: %v", err)
	}
	defer store.Close()

	count, err := RefreshModelPricing(context.Background(), store, OpenRouterPricingFetcher{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("refresh pricing: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 pricing record, got %d", count)
	}
	got, err := store.GetModelPricing(context.Background(), "openrouter", "x-ai/grok-build-0.1")
	if err != nil {
		t.Fatalf("get pricing: %v", err)
	}
	if got.PromptCostPerToken != 0.0000004 || got.CompletionCostPerToken != 0.0000012 {
		t.Fatalf("unexpected pricing: %#v", got)
	}
}

func TestSQLiteModelPricingStoreUpsertsAndFindsPrefixedModelIDs(t *testing.T) {
	store, err := NewSQLiteModelPricingStore(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatalf("new pricing store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertModelPricing(context.Background(), []ModelPricingRecord{
		{
			Provider:               "openrouter",
			ModelID:                "x-ai/grok-build-0.1",
			PromptCostPerToken:     0.0000004,
			CompletionCostPerToken: 0.0000012,
			Source:                 "openrouter",
		},
	}); err != nil {
		t.Fatalf("upsert pricing: %v", err)
	}

	got, err := store.GetModelPricing(context.Background(), "openrouter", "openrouter/x-ai/grok-build-0.1")
	if err != nil {
		t.Fatalf("get prefixed pricing: %v", err)
	}
	if got.ModelID != "x-ai/grok-build-0.1" {
		t.Fatalf("expected normalized model id, got %q", got.ModelID)
	}
	if got.PromptCostPerToken != 0.0000004 || got.CompletionCostPerToken != 0.0000012 {
		t.Fatalf("unexpected pricing: %#v", got)
	}
}

// TestModelPricingUniversalPriceBook verifies that prices fetched from the
// OpenRouter catalog (always stored under provider "openrouter" with a
// "<upstream>/<model>" id) resolve for direct-provider routes such as Bifrost,
// so cost telemetry is not zero for non-OpenRouter providers.
func TestModelPricingUniversalPriceBook(t *testing.T) {
	store, err := NewSQLiteModelPricingStore(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatalf("new pricing store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertModelPricing(context.Background(), []ModelPricingRecord{
		{
			Provider:               "openrouter",
			ModelID:                "anthropic/claude-haiku-4.5",
			PromptCostPerToken:     0.0000008,
			CompletionCostPerToken: 0.000004,
			Source:                 "openrouter",
		},
	}); err != nil {
		t.Fatalf("upsert pricing: %v", err)
	}

	cases := []struct {
		name     string
		provider string
		modelID  string
	}{
		{name: "direct provider, bare model", provider: "anthropic", modelID: "claude-haiku-4.5"},
		{name: "direct provider, qualified model", provider: "anthropic", modelID: "anthropic/claude-haiku-4.5"},
		{name: "mixed case", provider: "Anthropic", modelID: "Claude-Haiku-4.5"},
		{name: "openrouter route", provider: "openrouter", modelID: "anthropic/claude-haiku-4.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.GetModelPricing(context.Background(), tc.provider, tc.modelID)
			if err != nil {
				t.Fatalf("expected price book hit for %s/%s, got error: %v", tc.provider, tc.modelID, err)
			}
			if got.PromptCostPerToken != 0.0000008 || got.CompletionCostPerToken != 0.000004 {
				t.Fatalf("unexpected pricing: %#v", got)
			}
		})
	}

	if _, err := store.GetModelPricing(context.Background(), "anthropic", "no-such-model"); err == nil {
		t.Fatal("expected ErrModelPricingNotFound for an unknown model")
	}
}

// TestPricingBackoffDelayCap verifies exponential growth is exact away from
// the cap (jitter=1 -> multiplier 1.0), and clamped once it would exceed the
// configured cap.
func TestPricingBackoffDelayCap(t *testing.T) {
	b := pricingBackoff{Base: 30 * time.Second, Factor: 2, Cap: 15 * time.Minute, MaxAttempts: 6, Jitter: func() float64 { return 1 }}
	if got := b.delay(1); got != 30*time.Second {
		t.Fatalf("expected 30s for attempt 1, got %s", got)
	}
	if got := b.delay(3); got != 2*time.Minute {
		t.Fatalf("expected 120s for attempt 3, got %s", got)
	}
	if got := b.delay(10); got != 15*time.Minute {
		t.Fatalf("expected attempt 10 clamped to the 15m cap, got %s", got)
	}
}

// TestPricingBackoffDelayJitterRange verifies jitter scales the delay within
// [0.5x, 1.0x] of the raw exponential value instead of ever exceeding it.
func TestPricingBackoffDelayJitterRange(t *testing.T) {
	b := pricingBackoff{Base: 30 * time.Second, Factor: 2, Cap: 15 * time.Minute, MaxAttempts: 6, Jitter: func() float64 { return 0 }}
	if got := b.delay(1); got != 15*time.Second {
		t.Fatalf("expected half of base at jitter=0, got %s", got)
	}
}

// TestRunModelPricingCycleRetriesUpToMaxAttemptsThenGivesUp verifies a
// persistently failing fetch is retried exactly MaxAttempts times, with a
// backoff sleep (never a real sleep, since Sleep is injected) between each
// pair of attempts, and a numbered log line per failed attempt.
func TestRunModelPricingCycleRetriesUpToMaxAttemptsThenGivesUp(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	store, err := NewSQLiteModelPricingStore(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatalf("new pricing store: %v", err)
	}
	defer store.Close()

	var sleeps []time.Duration
	var logs []string
	backoff := pricingBackoff{
		Base: 30 * time.Second, Factor: 2, Cap: 15 * time.Minute, MaxAttempts: 3,
		Sleep: func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
		Jitter: func() float64 { return 0 },
	}
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	if _, err := runModelPricingCycle(context.Background(), store, OpenRouterPricingFetcher{BaseURL: server.URL}, backoff, logf); err == nil {
		t.Fatal("expected the cycle to exhaust retries and return an error")
	}
	if requests != 3 {
		t.Fatalf("expected 3 fetch attempts, got %d", requests)
	}
	if len(sleeps) != 2 {
		t.Fatalf("expected 2 backoff sleeps between 3 attempts, got %d: %v", len(sleeps), sleeps)
	}
	if sleeps[0] != 15*time.Second || sleeps[1] != 30*time.Second {
		t.Fatalf("unexpected backoff durations: %v", sleeps)
	}
	if len(logs) != 3 {
		t.Fatalf("expected a log line per failed attempt, got %v", logs)
	}
	for i, want := range []string{"attempt 1/3", "attempt 2/3", "attempt 3/3"} {
		if !strings.Contains(logs[i], want) {
			t.Fatalf("expected log %d to mention %q, got %q", i, want, logs[i])
		}
	}
}

// TestRunModelPricingCycleSucceedsAfterRetryAndResetsNextCycle verifies a
// cycle that needed one retry to succeed does not leak its attempt counter
// into the next cycle: a fresh call starts back at attempt 1 regardless of
// how many requests the server has seen overall (Req: reset on success).
func TestRunModelPricingCycleSucceedsAfterRetryAndResetsNextCycle(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		n := requestCount
		mu.Unlock()
		// The first request of each cycle (global request 1, then 3) fails;
		// the rest succeed.
		if n == 1 || n == 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"x-ai/grok-build-0.1","pricing":{"prompt":"0.0000004","completion":"0.0000012"}}]}`))
	}))
	defer server.Close()

	store, err := NewSQLiteModelPricingStore(filepath.Join(t.TempDir(), "pricing.db"))
	if err != nil {
		t.Fatalf("new pricing store: %v", err)
	}
	defer store.Close()
	fetcher := OpenRouterPricingFetcher{BaseURL: server.URL}

	runCycle := func() ([]time.Duration, []string, int, error) {
		var sleeps []time.Duration
		var logs []string
		backoff := pricingBackoff{
			Base: 30 * time.Second, Factor: 2, Cap: 15 * time.Minute, MaxAttempts: 6,
			Sleep: func(ctx context.Context, d time.Duration) error {
				sleeps = append(sleeps, d)
				return nil
			},
			Jitter: func() float64 { return 0 },
		}
		logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
		count, err := runModelPricingCycle(context.Background(), store, fetcher, backoff, logf)
		return sleeps, logs, count, err
	}

	sleepsA, logsA, countA, errA := runCycle()
	if errA != nil {
		t.Fatalf("expected cycle A to succeed after one retry, got %v", errA)
	}
	if countA != 1 || len(sleepsA) != 1 {
		t.Fatalf("expected cycle A to record 1 pricing entry after 1 sleep, got count=%d sleeps=%v", countA, sleepsA)
	}
	if len(logsA) != 1 || !strings.Contains(logsA[0], "attempt 1/6") {
		t.Fatalf("expected cycle A's single failure logged as attempt 1/6, got %v", logsA)
	}

	sleepsB, logsB, countB, errB := runCycle()
	if errB != nil {
		t.Fatalf("expected cycle B to succeed after one retry, got %v", errB)
	}
	if countB != 1 || len(sleepsB) != 1 {
		t.Fatalf("expected cycle B to record 1 pricing entry after 1 sleep, got count=%d sleeps=%v", countB, sleepsB)
	}
	if len(logsB) != 1 || !strings.Contains(logsB[0], "attempt 1/6") {
		t.Fatalf("expected cycle B's attempt counter to reset to attempt 1/6 despite prior cycles, got %v", logsB)
	}
}
