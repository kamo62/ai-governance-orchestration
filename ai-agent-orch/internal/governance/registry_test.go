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
	h := NewRegistryHandler(store, svc)

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
	h := NewRegistryHandler(store, svc)

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
	svc := NewSessionService(SessionConfig{DevToken: "test"})
	h := NewRegistryHandler(store, svc)

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
}

func TestRegistryHandler_MaturityExports(t *testing.T) {
	store := NewRegistryStore()
	svc := NewSessionService(SessionConfig{DevToken: "test"})
	h := NewRegistryHandler(store, svc)

	store.AppendExport(MaturityExportRecord{
		SessionID:      "sess_1",
		EventType:      "session.summary",
		Actor:          "local-dev",
		UseCaseID:      "uc_test",
		PolicyDecision: "allowed",
		RecordedAt:     time.Now().UTC(),
	})

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
