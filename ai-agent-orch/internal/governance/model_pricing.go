package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
)

var ErrModelPricingNotFound = errors.New("model pricing not found")

type ModelPricingRecord struct {
	Provider               string    `json:"provider"`
	ModelID                string    `json:"model_id"`
	PromptCostPerToken     float64   `json:"prompt_cost_per_token"`
	CompletionCostPerToken float64   `json:"completion_cost_per_token"`
	Source                 string    `json:"source"`
	FetchedAt              time.Time `json:"fetched_at"`
}

type ModelPricingStore interface {
	UpsertModelPricing(ctx context.Context, records []ModelPricingRecord) error
	GetModelPricing(ctx context.Context, provider string, modelID string) (ModelPricingRecord, error)
}

type SQLiteModelPricingStore struct {
	db *sql.DB
}

func NewSQLiteModelPricingStore(dbPath string) (*SQLiteModelPricingStore, error) {
	if dbPath == "" {
		return nil, errors.New("sqlite model pricing db path is required")
	}
	db, err := sqlitex.Open(dbPath, "model pricing")
	if err != nil {
		return nil, err
	}
	store := &SQLiteModelPricingStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate model pricing: %w", err)
	}
	return store, nil
}

func (s *SQLiteModelPricingStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS model_pricing (
			provider TEXT NOT NULL,
			model_id TEXT NOT NULL,
			prompt_cost_per_token REAL NOT NULL,
			completion_cost_per_token REAL NOT NULL,
			source TEXT NOT NULL,
			fetched_at TEXT NOT NULL,
			PRIMARY KEY (provider, model_id)
		);
		CREATE INDEX IF NOT EXISTS idx_model_pricing_fetched_at ON model_pricing(fetched_at);
	`)
	return err
}

func (s *SQLiteModelPricingStore) UpsertModelPricing(ctx context.Context, records []ModelPricingRecord) error {
	if s == nil || s.db == nil {
		return errors.New("model pricing store unavailable")
	}
	if len(records) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin model pricing tx: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO model_pricing (
			provider, model_id, prompt_cost_per_token, completion_cost_per_token, source, fetched_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, model_id) DO UPDATE SET
			prompt_cost_per_token = excluded.prompt_cost_per_token,
			completion_cost_per_token = excluded.completion_cost_per_token,
			source = excluded.source,
			fetched_at = excluded.fetched_at
	`)
	if err != nil {
		return fmt.Errorf("prepare model pricing upsert: %w", err)
	}
	defer stmt.Close()
	for _, record := range records {
		provider := normalizePricingProvider(record.Provider)
		modelID := normalizePricingModelID(provider, record.ModelID)
		if provider == "" || modelID == "" {
			continue
		}
		fetchedAt := record.FetchedAt
		if fetchedAt.IsZero() {
			fetchedAt = time.Now().UTC()
		}
		if _, err := stmt.ExecContext(ctx, provider, modelID, record.PromptCostPerToken, record.CompletionCostPerToken, defaultString(record.Source, "openrouter"), fetchedAt.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("upsert model pricing %s/%s: %w", provider, modelID, err)
		}
	}
	return tx.Commit()
}

func (s *SQLiteModelPricingStore) GetModelPricing(ctx context.Context, provider string, modelID string) (ModelPricingRecord, error) {
	if s == nil || s.db == nil {
		return ModelPricingRecord{}, ErrModelPricingNotFound
	}
	for _, lookup := range pricingLookups(provider, modelID) {
		var record ModelPricingRecord
		var fetchedAt string
		err := s.db.QueryRowContext(ctx, `
			SELECT provider, model_id, prompt_cost_per_token, completion_cost_per_token, source, fetched_at
			FROM model_pricing
			WHERE provider = ? AND model_id = ?
		`, lookup.provider, lookup.modelID).Scan(&record.Provider, &record.ModelID, &record.PromptCostPerToken, &record.CompletionCostPerToken, &record.Source, &fetchedAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return ModelPricingRecord{}, fmt.Errorf("get model pricing: %w", err)
		}
		record.FetchedAt, _ = time.Parse(time.RFC3339Nano, fetchedAt)
		return record, nil
	}
	return ModelPricingRecord{}, ErrModelPricingNotFound
}

func (s *SQLiteModelPricingStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

type OpenRouterPricingFetcher struct {
	BaseURL    string
	HTTPClient *http.Client
}

func (f OpenRouterPricingFetcher) Fetch(ctx context.Context) ([]ModelPricingRecord, error) {
	baseURL := strings.TrimRight(f.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	client := f.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create OpenRouter pricing request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch OpenRouter pricing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("OpenRouter pricing returned %d", resp.StatusCode)
	}
	var payload openRouterModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode OpenRouter pricing: %w", err)
	}
	fetchedAt := time.Now().UTC()
	records := make([]ModelPricingRecord, 0, len(payload.Data))
	for _, model := range payload.Data {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			continue
		}
		records = append(records, ModelPricingRecord{
			Provider:               "openrouter",
			ModelID:                modelID,
			PromptCostPerToken:     parsePricingFloat(model.Pricing.Prompt),
			CompletionCostPerToken: parsePricingFloat(model.Pricing.Completion),
			Source:                 "openrouter",
			FetchedAt:              fetchedAt,
		})
	}
	return records, nil
}

type openRouterModelsResponse struct {
	Data []struct {
		ID      string `json:"id"`
		Pricing struct {
			Prompt     any `json:"prompt"`
			Completion any `json:"completion"`
		} `json:"pricing"`
	} `json:"data"`
}

func RefreshModelPricing(ctx context.Context, store ModelPricingStore, fetcher OpenRouterPricingFetcher) (int, error) {
	records, err := fetcher.Fetch(ctx)
	if err != nil {
		return 0, err
	}
	if err := store.UpsertModelPricing(ctx, records); err != nil {
		return 0, err
	}
	return len(records), nil
}

// pricingBackoff bounds the retry applied to a single refresh cycle (the
// synchronous startup run, or one ticker tick) before giving up and waiting
// for the next regular tick. Sleep and Jitter are injectable so tests can
// drive every attempt without a real clock.
type pricingBackoff struct {
	Base        time.Duration
	Factor      float64
	Cap         time.Duration
	MaxAttempts int
	// Sleep waits for d, or returns ctx.Err() if ctx is done first.
	Sleep func(ctx context.Context, d time.Duration) error
	// Jitter returns a value in [0, 1); nil defaults to a fixed midpoint.
	Jitter func() float64
}

func defaultPricingBackoff() pricingBackoff {
	return pricingBackoff{
		Base:        30 * time.Second,
		Factor:      2,
		Cap:         15 * time.Minute,
		MaxAttempts: 6,
		Sleep: func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
		Jitter: rand.Float64,
	}
}

// delay returns the backoff duration to wait after a failed attempt number
// (1-indexed), as min(cap, base*factor^(attempt-1)) scaled by a jitter factor
// in [0.5, 1.0] so retries spread out instead of firing in lockstep, while
// never exceeding the cap.
func (b pricingBackoff) delay(attempt int) time.Duration {
	factor := b.Factor
	if factor <= 0 {
		factor = 2
	}
	raw := float64(b.Base) * math.Pow(factor, float64(attempt-1))
	capped := raw
	if b.Cap > 0 && capped > float64(b.Cap) {
		capped = float64(b.Cap)
	}
	jitter := 0.5
	if b.Jitter != nil {
		jitter = b.Jitter()
	}
	switch {
	case jitter < 0:
		jitter = 0
	case jitter > 1:
		jitter = 1
	}
	return time.Duration(capped * (0.5 + 0.5*jitter))
}

// runModelPricingCycle attempts a refresh up to backoff.MaxAttempts times,
// waiting backoff.delay(attempt) between failures (jittered, capped) and
// logging each failed attempt with its number. It returns the model count on
// success, or the last error once every attempt in the cycle is exhausted;
// the caller is expected to simply wait for the next regular tick after that.
func runModelPricingCycle(ctx context.Context, store ModelPricingStore, fetcher OpenRouterPricingFetcher, backoff pricingBackoff, logf func(string, ...any)) (int, error) {
	maxAttempts := backoff.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		count, err := RefreshModelPricing(refreshCtx, store, fetcher)
		cancel()
		if err == nil {
			return count, nil
		}
		lastErr = err
		if logf != nil {
			logf("model pricing refresh attempt %d/%d failed: %v", attempt, maxAttempts, err)
		}
		if attempt == maxAttempts {
			break
		}
		if sleepErr := backoff.Sleep(ctx, backoff.delay(attempt)); sleepErr != nil {
			return 0, sleepErr
		}
	}
	return 0, lastErr
}

func StartModelPricingRefresh(ctx context.Context, store ModelPricingStore, fetcher OpenRouterPricingFetcher, interval time.Duration, runImmediately bool, logf func(string, ...any)) {
	if store == nil {
		return
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	backoff := defaultPricingBackoff()
	go func() {
		run := func() {
			count, err := runModelPricingCycle(ctx, store, fetcher, backoff, logf)
			if err != nil {
				if logf != nil {
					logf("model pricing refresh exhausted retries, waiting for next tick: %v", err)
				}
				return
			}
			if logf != nil {
				logf("model pricing refreshed: %d models", count)
			}
		}
		if runImmediately {
			run()
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}

type pricingLookup struct {
	provider string
	modelID  string
}

// pricingLookups returns the ordered (provider, model_id) pairs to try when
// resolving a price. It first tries the exact provider, then falls back to the
// OpenRouter price book, which is treated as a universal catalog keyed by
// "<upstream-provider>/<model>". List prices for a given model are effectively
// the same regardless of how the request is routed, so Bifrost-backed provider
// routes reuse the OpenRouter catalog for the same underlying model instead of
// reporting zero cost.
func pricingLookups(provider string, modelID string) []pricingLookup {
	provider = normalizePricingProvider(provider)
	model := normalizePricingModelID(provider, modelID)
	if model == "" {
		return nil
	}
	var lookups []pricingLookup
	seen := make(map[pricingLookup]struct{})
	add := func(p, m string) {
		if p == "" || m == "" {
			return
		}
		key := pricingLookup{provider: p, modelID: m}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		lookups = append(lookups, key)
	}
	// 1. Exact provider, bare and provider-qualified model id.
	add(provider, model)
	add(provider, provider+"/"+model)
	// 2. OpenRouter universal price book fallback.
	if provider != "openrouter" {
		add("openrouter", provider+"/"+model)
	}
	add("openrouter", model)
	// 3. Vendor-prefixed OpenRouter fallback for per-user Copilot routes. Copilot
	// reports bare upstream ids (for example "gemini-3.5-flash"), while the
	// OpenRouter price book keys models as "<vendor>/<model>". The list price for
	// a model is the same regardless of being served via Copilot, so map the bare
	// id to its OpenRouter equivalent to price Copilot routes.
	if vendorModel := openRouterVendorModel(model); vendorModel != "" {
		add("openrouter", vendorModel)
	}
	return lookups
}

// openRouterVendorModel maps a bare upstream model id (as Copilot reports it) to
// the vendor-prefixed id used by the OpenRouter price book. Returns "" when the
// vendor cannot be inferred.
func openRouterVendorModel(model string) string {
	model = strings.Trim(strings.ToLower(model), "/ ")
	if model == "" || strings.Contains(model, "/") {
		return ""
	}
	switch {
	case strings.HasPrefix(model, "claude-"):
		return "anthropic/" + model
	case strings.HasPrefix(model, "gemini-"):
		return "google/" + model
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
		return "openai/" + model
	default:
		return ""
	}
}

func normalizePricingProvider(provider string) string {
	provider = strings.Trim(strings.ToLower(provider), "/ ")
	if provider == "" {
		return "openrouter"
	}
	return provider
}

func normalizePricingModelID(provider string, modelID string) string {
	modelID = strings.Trim(strings.ToLower(modelID), "/ ")
	provider = normalizePricingProvider(provider)
	if provider != "" && strings.HasPrefix(modelID, provider+"/") {
		modelID = strings.TrimPrefix(modelID, provider+"/")
	}
	return modelID
}

func parsePricingFloat(value any) float64 {
	switch typed := value.(type) {
	case string:
		n, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return n
	case float64:
		return typed
	case int:
		return float64(typed)
	case json.Number:
		n, _ := typed.Float64()
		return n
	default:
		return 0
	}
}
