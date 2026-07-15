package governance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestMaturityExportRunProjectsSessionFields(t *testing.T) {
	h := newInsightTestHarness(t)
	now := time.Now().UTC()

	createTestSession(t, h, SessionRecord{
		SessionID:           "sess_1",
		ActorSubject:        "local-dev",
		Agent:               "code-review",
		Classification:      "internal",
		PromptSHA256:        "abc",
		Status:              "done",
		CreatedAt:           now.Add(-time.Hour),
		UseCaseID:           "uc_1",
		WorkflowID:          "wf_1",
		WorkItemID:          "wi_1",
		RepoURL:             "https://example.com/repo",
		Branch:              "main",
		CommitSHA:           "deadbeef",
		BaselineCostUSD:     10,
		ToolCostUSD:         1,
		PlatformCostUSD:     2,
		ReviewCostUSD:       3,
		VerificationCostUSD: 4,
		RetryCount:          2,
	})
	recordTestPolicyDecision(t, h, PolicyDecisionRecord{
		DecisionID: "pol_1", SessionID: "sess_1", Actor: "local-dev",
		Allowed: false, Reason: "cost cap exceeded", CostCapUSD: 5, EstimatedCostUSD: 8,
		RecordedAt: now.Add(-50 * time.Minute),
	})
	if _, err := h.service.audit.Append(context.Background(), audit.Event{
		EventID: "evt_patch", SessionID: "sess_1", EventType: "patch.decision",
		PatchDecision: "applied", RecordedAt: now.Add(-40 * time.Minute),
	}); err != nil {
		t.Fatalf("append patch decision event: %v", err)
	}
	if _, err := h.service.audit.Append(context.Background(), audit.Event{
		EventID: "evt_call", SessionID: "sess_1", EventType: "model.proxy_call",
		TokenUsage: map[string]any{"prompt_tokens": 100, "completion_tokens": 50, "cost_usd": 0.75},
		RecordedAt: now.Add(-45 * time.Minute),
	}); err != nil {
		t.Fatalf("append model call event: %v", err)
	}
	for i, status := range []string{"confirmed", "candidate", "proposed", "confirmed"} {
		if _, _, err := h.registry.AppendEvidence(EvidenceRecord{
			ID: "ev_" + string(rune('0'+i)), SessionID: "sess_1", EvidenceType: "test",
			Description: "d", Status: status, RecordedAt: now,
		}); err != nil {
			t.Fatalf("seed evidence %d: %v", i, err)
		}
	}

	handler := NewMaturityExportRunHandler(MaturityExportRunConfig{Service: h.service, Registry: h.registry, Now: func() time.Time { return now }})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/reporting/maturity-export/run", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response MaturityExportRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Appended != 1 || response.Skipped != 0 {
		t.Fatalf("expected appended=1 skipped=0, got %#v", response)
	}

	exports, err := h.registry.Exports()
	if err != nil {
		t.Fatalf("exports: %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("expected exactly one export row, got %d: %#v", len(exports), exports)
	}
	got := exports[0]
	if got.SessionID != "sess_1" || got.Actor != "local-dev" || got.UseCaseID != "uc_1" || got.WorkflowID != "wf_1" || got.WorkItemID != "wi_1" {
		t.Fatalf("identity fields not projected: %#v", got)
	}
	if got.Repository != "https://example.com/repo" || got.Branch != "main" || got.Commit != "deadbeef" {
		t.Fatalf("repo fields not projected: %#v", got)
	}
	if got.FinalStatus != "done" || got.Agent != "code-review" || got.Classification != "internal" {
		t.Fatalf("status/agent/classification not projected: %#v", got)
	}
	if got.PolicyDecision != "denied" || got.PolicyReason != "cost cap exceeded" {
		t.Fatalf("policy decision/reason not projected: %#v", got)
	}
	if got.CostCapResult != "exceeded" {
		t.Fatalf("expected cost cap result exceeded, got %q", got.CostCapResult)
	}
	if got.PatchDecision != "applied" {
		t.Fatalf("expected patch decision applied, got %q", got.PatchDecision)
	}
	if got.EvidenceCompleteness != 0.75 {
		t.Fatalf("expected evidence completeness 0.75 (3 of 4 confirmed/candidate), got %v", got.EvidenceCompleteness)
	}
	if got.ModelCostUSD != 0.75 {
		t.Fatalf("expected model cost from usage summary 0.75, got %v", got.ModelCostUSD)
	}
	if got.BaselineCostUSD != 10 || got.ToolCostUSD != 1 || got.PlatformCostUSD != 2 || got.ReviewCostUSD != 3 || got.VerificationCostUSD != 4 || got.RetryCount != 2 {
		t.Fatalf("session-sizing cost fields not projected: %#v", got)
	}
	if !got.RecordedAt.Equal(now) {
		t.Fatalf("expected recorded_at pinned to run time %v, got %v", now, got.RecordedAt)
	}
}

func TestMaturityExportRunIsIdempotentForSameWindowEndDate(t *testing.T) {
	h := newInsightTestHarness(t)
	now := time.Now().UTC()
	createTestSession(t, h, SessionRecord{SessionID: "sess_1", ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "a", Status: "done", CreatedAt: now.Add(-time.Hour)})
	createTestSession(t, h, SessionRecord{SessionID: "sess_2", ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "b", Status: "done", CreatedAt: now.Add(-2 * time.Hour)})

	handler := NewMaturityExportRunHandler(MaturityExportRunConfig{Service: h.service, Registry: h.registry, Now: func() time.Time { return now }})

	first := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/v1/admin/reporting/maturity-export/run", nil)
	req1.Header.Set("Authorization", "Bearer admin-token")
	handler.ServeHTTP(first, req1)
	if first.Code != http.StatusOK {
		t.Fatalf("first run: expected 200, got %d: %s", first.Code, first.Body.String())
	}
	var firstResponse MaturityExportRunResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstResponse); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if firstResponse.Appended != 2 || firstResponse.Skipped != 0 {
		t.Fatalf("first run: expected appended=2 skipped=0, got %#v", firstResponse)
	}

	second := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/admin/reporting/maturity-export/run", nil)
	req2.Header.Set("Authorization", "Bearer admin-token")
	handler.ServeHTTP(second, req2)
	if second.Code != http.StatusOK {
		t.Fatalf("second run: expected 200, got %d: %s", second.Code, second.Body.String())
	}
	var secondResponse MaturityExportRunResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondResponse); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if secondResponse.Appended != 0 || secondResponse.Skipped != 2 {
		t.Fatalf("second run: expected appended=0 skipped=2 (idempotent replay), got %#v", secondResponse)
	}

	exports, err := h.registry.Exports()
	if err != nil {
		t.Fatalf("exports: %v", err)
	}
	if len(exports) != 2 {
		t.Fatalf("expected exactly 2 export rows after two runs on the same window end date, got %d", len(exports))
	}
}

func TestMaturityExportRunSerializesConcurrentPosts(t *testing.T) {
	h := newInsightTestHarness(t)
	now := time.Now().UTC()
	for _, id := range []string{"sess_1", "sess_2"} {
		createTestSession(t, h, SessionRecord{SessionID: id, ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: id, Status: "done", CreatedAt: now.Add(-time.Hour)})
	}
	handler := NewMaturityExportRunHandler(MaturityExportRunConfig{Service: h.service, Registry: h.registry, Now: func() time.Time { return now }})

	responses := make(chan MaturityExportRunResponse, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/admin/reporting/maturity-export/run", nil)
			req.Header.Set("Authorization", "Bearer admin-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
				return
			}
			var response MaturityExportRunResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Errorf("decode response: %v", err)
				return
			}
			responses <- response
		}()
	}
	wg.Wait()
	close(responses)
	appended := 0
	for response := range responses {
		appended += response.Appended
	}
	exports, err := h.registry.Exports()
	if err != nil {
		t.Fatal(err)
	}
	if appended != 2 || len(exports) != 2 {
		t.Fatalf("expected two sessions appended once, appended=%d exports=%#v", appended, exports)
	}
}

func TestMaturityExportRunRequiresAdmin(t *testing.T) {
	h := newInsightTestHarness(t)
	handler := NewMaturityExportRunHandler(MaturityExportRunConfig{Service: h.service, Registry: h.registry})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/reporting/maturity-export/run", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMaturityExportRunRejectsGet(t *testing.T) {
	h := newInsightTestHarness(t)
	handler := NewMaturityExportRunHandler(MaturityExportRunConfig{Service: h.service, Registry: h.registry})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/reporting/maturity-export/run", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET (the snapshot must never be triggerable from a GET), got %d: %s", rec.Code, rec.Body.String())
	}
}
