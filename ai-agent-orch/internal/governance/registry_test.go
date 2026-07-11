package governance

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRegistryHandler_CreateAndGetUseCase(t *testing.T) {
	store := NewRegistryStore()
	svc := NewSessionService(SessionConfig{DevToken: "test"})
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

	// Create.
	body, _ := json.Marshal(map[string]any{
		"id":               "uc_test",
		"owner":            "local-dev",
		"domain":           "engineering",
		"expected_benefit": "faster tests",
		"classification":   "internal",
		"risk_level":       "medium",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/use-cases", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Get.
	req = httptest.NewRequest(http.MethodGet, "/v1/use-cases/uc_test", nil)
	req.Header.Set("Authorization", "Bearer test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var uc UseCase
	if err := json.Unmarshal(w.Body.Bytes(), &uc); err != nil {
		t.Fatalf("decode use case: %v", err)
	}
	if uc.ID != "uc_test" {
		t.Fatalf("unexpected use case id: %s", uc.ID)
	}
	if uc.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}
}

func TestRegistryHandler_CreateAndGetWorkflow(t *testing.T) {
	store := NewRegistryStore()
	svc := NewSessionService(SessionConfig{DevToken: "test"})
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

	body, _ := json.Marshal(map[string]any{
		"id":          "wf_devsecops",
		"name":        "DevSecOps Review",
		"description": "Security review workflow",
		"stages":      []string{"scan", "review", "approve"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/workflows", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var wf Workflow
	if err := json.Unmarshal(w.Body.Bytes(), &wf); err != nil {
		t.Fatalf("decode workflow: %v", err)
	}
	if wf.ID != "wf_devsecops" {
		t.Fatalf("unexpected workflow id: %s", wf.ID)
	}
	if len(wf.Stages) != 3 {
		t.Fatalf("unexpected stages: %v", wf.Stages)
	}
}

func TestRegistryHandler_CreateAndGetManifest(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestService(t, "sess_a", "local-dev")
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

	body, _ := json.Marshal(map[string]any{
		"id":               "ctx_1",
		"session_id":       "sess_a",
		"summary":          "bounded context for test",
		"source_system":    "jira",
		"source_object_id": "JIRA-1234",
		"actor":            "local-dev",
		"auth_scope":       "read",
		"classification":   "internal",
		"cache_status":     "miss",
		"influenced_model": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/context-manifests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/context-manifests/ctx_1", nil)
	req.Header.Set("Authorization", "Bearer test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var m ContextManifest
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.ID != "ctx_1" {
		t.Fatalf("unexpected manifest id: %s", m.ID)
	}
	if !m.InfluencedModel {
		t.Fatal("expected influenced_model to be true")
	}
	if m.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}
}

func TestRegistryHandler_ManifestRequiresSessionOwnership(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestServiceWithSessions(t, map[string]string{
		"sess_a":     "local-dev",
		"sess_other": "other-user",
	})
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

	body, _ := json.Marshal(map[string]any{
		"id":               "ctx_other",
		"session_id":       "sess_other",
		"summary":          "bounded context for another user",
		"source_system":    "jira",
		"source_object_id": "JIRA-999",
		"actor":            "local-dev",
		"auth_scope":       "read",
		"classification":   "internal",
		"cache_status":     "miss",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/context-manifests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owned manifest session, got %d: %s", w.Code, w.Body.String())
	}

	if err := store.CreateManifest(ContextManifest{
		ID:             "ctx_other",
		SessionID:      "sess_other",
		Summary:        "other user's context",
		SourceSystem:   "jira",
		SourceObjectID: "JIRA-999",
		Actor:          "other-user",
		AuthScope:      "read",
		FetchedAt:      time.Now().UTC(),
		Classification: "internal",
		CacheStatus:    "miss",
	}); err != nil {
		t.Fatalf("seed manifest: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/context-manifests/ctx_other", nil)
	req.Header.Set("Authorization", "Bearer test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owned manifest read, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRegistryHandler_MaturityExports(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestServiceWithSessions(t, map[string]string{
		"sess_1":     "local-dev",
		"sess_other": "other-user",
	})
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

	if err := store.AppendExport(MaturityExportRecord{
		SessionID:      "sess_1",
		EventType:      "session.summary",
		Actor:          "local-dev",
		UseCaseID:      "uc_test",
		PolicyDecision: "allowed",
		RecordedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append export: %v", err)
	}
	if err := store.AppendExport(MaturityExportRecord{
		SessionID:      "sess_other",
		EventType:      "session.summary",
		Actor:          "other-user",
		UseCaseID:      "uc_other",
		PolicyDecision: "allowed",
		RecordedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append other export: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/reporting/maturity-governance", nil)
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Exports []MaturityExportRecord `json:"exports"`
		Count   int                    `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("expected 1 export, got %d", resp.Count)
	}
	if resp.Exports[0].SessionID != "sess_1" {
		t.Fatalf("unexpected session id: %s", resp.Exports[0].SessionID)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/reporting/maturity-governance", nil)
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-AI-Orch-Local-Identity", "other-user")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode other response: %v", err)
	}
	if resp.Count != 1 || resp.Exports[0].SessionID != "sess_other" {
		t.Fatalf("expected only other user's export, got %#v", resp)
	}
}

func TestRegistryStore_RegisterUseCaseValidation(t *testing.T) {
	store := NewRegistryStore()
	if err := store.RegisterUseCase(UseCase{}); err == nil {
		t.Fatal("expected validation error for empty use case")
	}
	if err := store.RegisterUseCase(UseCase{ID: "x", Owner: "o", Domain: "d", Classification: "c"}); err == nil {
		t.Fatal("expected validation error for missing risk_level")
	}
}

func TestRegistryStore_RegisterWorkflowValidation(t *testing.T) {
	store := NewRegistryStore()
	if err := store.RegisterWorkflow(Workflow{}); err == nil {
		t.Fatal("expected validation error for empty workflow")
	}
	if err := store.RegisterWorkflow(Workflow{ID: "x"}); err == nil {
		t.Fatal("expected validation error for missing name")
	}
}

// TestRegistryStore_AppendEvidenceDedupesClientEventID verifies the in-memory
// store mirrors the durable store's idempotency: a second append with the
// same (session_id, client_event_id) is reported as a duplicate of the
// original record instead of creating a second one, while a different
// session may reuse the same client_event_id without colliding.
func TestRegistryStore_AppendEvidenceDedupesClientEventID(t *testing.T) {
	store := NewRegistryStore()

	first, duplicate, err := store.AppendEvidence(EvidenceRecord{
		ID:            "ev_1",
		SessionID:     "sess_1",
		EvidenceType:  "test_result",
		Description:   "first attempt",
		ClientEventID: "cev_abc",
	})
	if err != nil {
		t.Fatalf("append first: %v", err)
	}
	if duplicate {
		t.Fatal("expected the first append to not be a duplicate")
	}

	retry, duplicate, err := store.AppendEvidence(EvidenceRecord{
		ID:            "ev_2",
		SessionID:     "sess_1",
		EvidenceType:  "test_result",
		Description:   "retried attempt",
		ClientEventID: "cev_abc",
	})
	if err != nil {
		t.Fatalf("append retry: %v", err)
	}
	if !duplicate {
		t.Fatal("expected the retried append to be reported as a duplicate")
	}
	if retry.ID != first.ID {
		t.Fatalf("expected the duplicate to return the original record %q, got %q", first.ID, retry.ID)
	}

	// A different session may reuse the same client_event_id.
	other, duplicate, err := store.AppendEvidence(EvidenceRecord{
		ID:            "ev_3",
		SessionID:     "sess_2",
		EvidenceType:  "test_result",
		Description:   "different session",
		ClientEventID: "cev_abc",
	})
	if err != nil {
		t.Fatalf("append other session: %v", err)
	}
	if duplicate {
		t.Fatal("expected a different session to not collide on the same client_event_id")
	}
	if other.ID != "ev_3" {
		t.Fatalf("unexpected id for other session's record: %s", other.ID)
	}

	all, err := store.Evidence()
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected exactly 2 stored records (no duplicate persisted), got %d", len(all))
	}
}
