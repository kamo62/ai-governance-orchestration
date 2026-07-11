package governance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

// --- write-recording store wrappers, shared by insight_handler_test.go and
// maturity_export_run_test.go, so a GET handler's "zero writes" claim is
// enforced rather than assumed. ---

type writeRecordingSessionStore struct {
	inner  *SQLiteSessionStore
	writes *int32
}

func (w *writeRecordingSessionStore) Create(ctx context.Context, rec SessionRecord) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.Create(ctx, rec)
}

func (w *writeRecordingSessionStore) Get(ctx context.Context, sessionID string) (SessionRecord, error) {
	return w.inner.Get(ctx, sessionID)
}

func (w *writeRecordingSessionStore) ListRecent(ctx context.Context, actorSubject string, limit int) ([]SessionRecord, error) {
	return w.inner.ListRecent(ctx, actorSubject, limit)
}

func (w *writeRecordingSessionStore) UpdateStatus(ctx context.Context, sessionID, status string) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.UpdateStatus(ctx, sessionID, status)
}

func (w *writeRecordingSessionStore) CompareAndSwapStatus(ctx context.Context, sessionID, from, to string) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.CompareAndSwapStatus(ctx, sessionID, from, to)
}

func (w *writeRecordingSessionStore) ListRecentSince(ctx context.Context, actorSubject string, since time.Time, limit int) ([]SessionRecord, error) {
	return w.inner.ListRecentSince(ctx, actorSubject, since, limit)
}

func (w *writeRecordingSessionStore) ListAllSince(ctx context.Context, since time.Time, max int) ([]SessionRecord, error) {
	return w.inner.ListAllSince(ctx, since, max)
}

type writeRecordingRegistryStore struct {
	inner  RegistryStoreInterface
	writes *int32
}

func (w *writeRecordingRegistryStore) RegisterUseCase(uc UseCase) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.RegisterUseCase(uc)
}
func (w *writeRecordingRegistryStore) GetUseCase(id string) (UseCase, bool) {
	return w.inner.GetUseCase(id)
}
func (w *writeRecordingRegistryStore) ListUseCases() ([]UseCase, error) {
	return w.inner.ListUseCases()
}

func (w *writeRecordingRegistryStore) RegisterWorkflow(wf Workflow) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.RegisterWorkflow(wf)
}
func (w *writeRecordingRegistryStore) GetWorkflow(id string) (Workflow, bool) {
	return w.inner.GetWorkflow(id)
}
func (w *writeRecordingRegistryStore) ListWorkflows() ([]Workflow, error) {
	return w.inner.ListWorkflows()
}

func (w *writeRecordingRegistryStore) CreateManifest(m ContextManifest) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.CreateManifest(m)
}
func (w *writeRecordingRegistryStore) GetManifest(id string) (ContextManifest, bool) {
	return w.inner.GetManifest(id)
}

func (w *writeRecordingRegistryStore) AppendCacheOutcome(c CacheOutcome) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.AppendCacheOutcome(c)
}
func (w *writeRecordingRegistryStore) CacheOutcomes() ([]CacheOutcome, error) {
	return w.inner.CacheOutcomes()
}

func (w *writeRecordingRegistryStore) AppendEvidence(e EvidenceRecord) (EvidenceRecord, bool, error) {
	atomic.AddInt32(w.writes, 1)
	return w.inner.AppendEvidence(e)
}
func (w *writeRecordingRegistryStore) Evidence() ([]EvidenceRecord, error) { return w.inner.Evidence() }

func (w *writeRecordingRegistryStore) TransitionEvidence(id string, to string, decision *EvidenceDecision) (EvidenceRecord, error) {
	atomic.AddInt32(w.writes, 1)
	return w.inner.TransitionEvidence(id, to, decision)
}

func (w *writeRecordingRegistryStore) AppendExport(r MaturityExportRecord) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.AppendExport(r)
}
func (w *writeRecordingRegistryStore) Exports() ([]MaturityExportRecord, error) {
	return w.inner.Exports()
}

type writeRecordingPolicyDecisionStore struct {
	inner  PolicyDecisionStore
	writes *int32
}

func (w *writeRecordingPolicyDecisionStore) RecordPolicyDecision(ctx context.Context, record PolicyDecisionRecord) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.RecordPolicyDecision(ctx, record)
}
func (w *writeRecordingPolicyDecisionStore) GetPolicyDecision(ctx context.Context, decisionID string) (PolicyDecisionRecord, bool, error) {
	return w.inner.GetPolicyDecision(ctx, decisionID)
}
func (w *writeRecordingPolicyDecisionStore) ListPolicyDecisions(ctx context.Context, sessionID string) ([]PolicyDecisionRecord, error) {
	return w.inner.ListPolicyDecisions(ctx, sessionID)
}

type writeRecordingKillSwitchStore struct {
	inner  KillSwitchStore
	writes *int32
}

func (w *writeRecordingKillSwitchStore) IsBlocked(scope, id string) bool {
	return w.inner.IsBlocked(scope, id)
}
func (w *writeRecordingKillSwitchStore) Block(scope, id string) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.Block(scope, id)
}
func (w *writeRecordingKillSwitchStore) Unblock(scope, id string) error {
	atomic.AddInt32(w.writes, 1)
	return w.inner.Unblock(scope, id)
}
func (w *writeRecordingKillSwitchStore) List() map[string][]string { return w.inner.List() }

// insightTestHarness bundles the stores an insight/maturity-export test
// needs, with every mutating store wrapped by a shared write counter.
type insightTestHarness struct {
	service  *SessionService
	registry *writeRecordingRegistryStore
	writes   *int32
}

func newInsightTestHarness(t *testing.T) insightTestHarness {
	t.Helper()
	dir := t.TempDir()
	sqliteSessions, err := NewSQLiteSessionStore(filepath.Join(dir, "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	t.Cleanup(func() { _ = sqliteSessions.Close() })

	writes := new(int32)
	sessions := &writeRecordingSessionStore{inner: sqliteSessions, writes: writes}
	registry := &writeRecordingRegistryStore{inner: NewRegistryStore(), writes: writes}
	policyDecisions := &writeRecordingPolicyDecisionStore{inner: NewMemoryPolicyDecisionStore(), writes: writes}
	killSwitch := &writeRecordingKillSwitchStore{inner: NewMemoryKillSwitch(), writes: writes}

	service := NewSessionService(SessionConfig{
		DevToken:        "dev-token",
		AdminToken:      "admin-token",
		Audit:           audit.NewFileStore(filepath.Join(dir, "audit.jsonl")),
		Sessions:        sessions,
		PolicyDecisions: policyDecisions,
		KillSwitchStore: killSwitch,
	})

	return insightTestHarness{service: service, registry: registry, writes: writes}
}

func (h insightTestHarness) resetWrites()      { atomic.StoreInt32(h.writes, 0) }
func (h insightTestHarness) writeCount() int32 { return atomic.LoadInt32(h.writes) }

func createTestSession(t *testing.T, h insightTestHarness, rec SessionRecord) {
	t.Helper()
	if err := h.service.sessions.Create(context.Background(), rec); err != nil {
		t.Fatalf("create session %s: %v", rec.SessionID, err)
	}
}

func recordTestPolicyDecision(t *testing.T, h insightTestHarness, rec PolicyDecisionRecord) {
	t.Helper()
	if err := h.service.policyDecisions.RecordPolicyDecision(context.Background(), rec); err != nil {
		t.Fatalf("record policy decision %s: %v", rec.DecisionID, err)
	}
}

func TestInsightHandlerDevTokenIsScopedToOwnActor(t *testing.T) {
	h := newInsightTestHarness(t)
	now := time.Now().UTC()
	createTestSession(t, h, SessionRecord{SessionID: "sess_mine", ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "a", Status: "done", CreatedAt: now})
	createTestSession(t, h, SessionRecord{SessionID: "sess_other", ActorSubject: "other-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "b", Status: "done", CreatedAt: now})
	recordTestPolicyDecision(t, h, PolicyDecisionRecord{DecisionID: "pol_mine", SessionID: "sess_mine", Actor: "local-dev", Allowed: false, Reason: "classification blocked", RecordedAt: now})
	recordTestPolicyDecision(t, h, PolicyDecisionRecord{DecisionID: "pol_other", SessionID: "sess_other", Actor: "other-dev", Allowed: false, Reason: "classification blocked", RecordedAt: now})

	handler := NewInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry})
	req := httptest.NewRequest(http.MethodGet, "/v1/reporting/governance-insights", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result InsightResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, item := range result.Items {
		if item.SessionID == "sess_other" {
			t.Fatalf("dev token leaked another actor's item: %#v", item)
		}
	}
	foundMine := false
	for _, item := range result.Items {
		if item.SessionID == "sess_mine" {
			foundMine = true
		}
	}
	if !foundMine {
		t.Fatalf("expected own-actor item in response, got %#v", result.Items)
	}
}

func TestInsightHandlerAdminSeesAllActors(t *testing.T) {
	h := newInsightTestHarness(t)
	now := time.Now().UTC()
	createTestSession(t, h, SessionRecord{SessionID: "sess_mine", ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "a", Status: "done", CreatedAt: now})
	createTestSession(t, h, SessionRecord{SessionID: "sess_other", ActorSubject: "other-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "b", Status: "done", CreatedAt: now})
	recordTestPolicyDecision(t, h, PolicyDecisionRecord{DecisionID: "pol_mine", SessionID: "sess_mine", Actor: "local-dev", Allowed: false, Reason: "classification blocked", RecordedAt: now})
	recordTestPolicyDecision(t, h, PolicyDecisionRecord{DecisionID: "pol_other", SessionID: "sess_other", Actor: "other-dev", Allowed: false, Reason: "classification blocked", RecordedAt: now})

	handler := NewAdminInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/reporting/governance-insights", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result InsightResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	seen := map[string]bool{}
	for _, item := range result.Items {
		seen[item.SessionID] = true
	}
	if !seen["sess_mine"] || !seen["sess_other"] {
		t.Fatalf("expected both actors' items org-wide, got %#v", result.Items)
	}
}

func TestInsightHandlerNonAdminRefusedOnAdminRoute(t *testing.T) {
	h := newInsightTestHarness(t)
	handler := NewAdminInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/reporting/governance-insights", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInsightHandlerDevRouteRequiresToken(t *testing.T) {
	h := newInsightTestHarness(t)
	handler := NewInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry})
	req := httptest.NewRequest(http.MethodGet, "/v1/reporting/governance-insights", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInsightHandlerWindowValidation(t *testing.T) {
	h := newInsightTestHarness(t)
	handler := NewInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry})

	for _, tc := range []struct {
		name   string
		window string
		want   int
	}{
		{name: "default when omitted", window: "", want: http.StatusOK},
		{name: "24h accepted", window: "24h", want: http.StatusOK},
		{name: "7d accepted", window: "7d", want: http.StatusOK},
		{name: "30d boundary accepted", window: "30d", want: http.StatusOK},
		{name: "31d rejected", window: "31d", want: http.StatusBadRequest},
		{name: "garbage rejected", window: "not-a-window", want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/reporting/governance-insights?window="+tc.window, nil)
			req.Header.Set("Authorization", "Bearer dev-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("window=%q: expected %d, got %d: %s", tc.window, tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestInsightHandlerLimitCapsItems(t *testing.T) {
	h := newInsightTestHarness(t)
	now := time.Now().UTC()
	// Status "created" avoids also tripping the stalled/no-evidence
	// categories, so this test isolates exactly one item per decision.
	for i := 0; i < 5; i++ {
		id := "pol_" + string(rune('a'+i))
		sessionID := "sess_" + string(rune('a'+i))
		createTestSession(t, h, SessionRecord{SessionID: sessionID, ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "x", Status: "created", CreatedAt: now})
		recordTestPolicyDecision(t, h, PolicyDecisionRecord{DecisionID: id, SessionID: sessionID, Actor: "local-dev", Allowed: false, Reason: "classification blocked", RecordedAt: now.Add(time.Duration(i) * time.Minute)})
	}

	handler := NewInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry})
	req := httptest.NewRequest(http.MethodGet, "/v1/reporting/governance-insights?limit=2", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result InsightResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected limit=2 to cap items, got %d: %#v", len(result.Items), result.Items)
	}
	// Summary counts reflect the full projection, not the truncated page.
	if result.Summary.Total != 5 {
		t.Fatalf("expected summary total 5 regardless of limit, got %d", result.Summary.Total)
	}
}

func TestInsightHandlerGetPerformsNoWrites(t *testing.T) {
	h := newInsightTestHarness(t)
	now := time.Now().UTC()
	createTestSession(t, h, SessionRecord{SessionID: "sess_1", ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "a", Status: "done", CreatedAt: now})
	recordTestPolicyDecision(t, h, PolicyDecisionRecord{DecisionID: "pol_1", SessionID: "sess_1", Actor: "local-dev", Allowed: false, Reason: "secret detected", RecordedAt: now})
	if _, _, err := h.registry.AppendEvidence(EvidenceRecord{ID: "ev_1", SessionID: "sess_1", EvidenceType: "test", Description: "d", Status: "confirmed", RecordedAt: now}); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	if err := h.service.killSwitchStore.Block("agent", "code-review"); err != nil {
		t.Fatalf("seed kill switch: %v", err)
	}

	h.resetWrites()

	devHandler := NewInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry})
	devReq := httptest.NewRequest(http.MethodGet, "/v1/reporting/governance-insights", nil)
	devReq.Header.Set("Authorization", "Bearer dev-token")
	devRec := httptest.NewRecorder()
	devHandler.ServeHTTP(devRec, devReq)
	if devRec.Code != http.StatusOK {
		t.Fatalf("dev route: expected 200, got %d: %s", devRec.Code, devRec.Body.String())
	}
	var devResult InsightResult
	if err := json.Unmarshal(devRec.Body.Bytes(), &devResult); err != nil {
		t.Fatalf("decode dev response: %v", err)
	}
	if devResult.Summary.Total == 0 {
		t.Fatalf("expected a non-empty projection to meaningfully exercise the read path, got %#v", devResult)
	}
	if got := h.writeCount(); got != 0 {
		t.Fatalf("dev route GET performed %d store writes, want 0", got)
	}

	h.resetWrites()

	adminHandler := NewAdminInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry})
	adminReq := httptest.NewRequest(http.MethodGet, "/v1/admin/reporting/governance-insights", nil)
	adminReq.Header.Set("Authorization", "Bearer admin-token")
	adminRec := httptest.NewRecorder()
	adminHandler.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin route: expected 200, got %d: %s", adminRec.Code, adminRec.Body.String())
	}
	if got := h.writeCount(); got != 0 {
		t.Fatalf("admin route GET performed %d store writes, want 0", got)
	}
}

func TestInsightHandlerDevSeesOnlyGlobalKillSwitches(t *testing.T) {
	h := newInsightTestHarness(t)
	if err := h.service.killSwitchStore.Block("global", "sessions"); err != nil {
		t.Fatal(err)
	}
	if err := h.service.killSwitchStore.Block("agent", "code-review"); err != nil {
		t.Fatal(err)
	}

	check := func(handler http.Handler, token string) InsightResult {
		req := httptest.NewRequest(http.MethodGet, "/v1/reporting/governance-insights", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var result InsightResult
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	dev := check(NewInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry}), "dev-token")
	admin := check(NewAdminInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry}), "admin-token")
	if hasInsightKey(dev.Items, "killswitch:agent:code-review") || !hasInsightKey(dev.Items, "killswitch:global:sessions") {
		t.Fatalf("dev kill switches leaked or omitted: %#v", dev.Items)
	}
	if !hasInsightKey(admin.Items, "killswitch:agent:code-review") || !hasInsightKey(admin.Items, "killswitch:global:sessions") {
		t.Fatalf("admin did not retain all scopes: %#v", admin.Items)
	}
}

func hasInsightKey(items []AttentionItem, key string) bool {
	for _, item := range items {
		if item.Key == key {
			return true
		}
	}
	return false
}

func TestInsightHandlerMethodNotAllowed(t *testing.T) {
	h := newInsightTestHarness(t)
	handler := NewInsightHandler(InsightHandlerConfig{Service: h.service, Registry: h.registry})
	req := httptest.NewRequest(http.MethodPost, "/v1/reporting/governance-insights", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rec.Code, rec.Body.String())
	}
}
