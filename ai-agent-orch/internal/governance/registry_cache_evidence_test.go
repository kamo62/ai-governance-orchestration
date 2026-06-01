package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ai-agent-orch/internal/audit"
)

func TestRegistryHandler_CreateAndListCacheOutcome(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestService(t, "sess_1", "local-dev")
	h := NewRegistryHandler(store, svc)

	body, _ := json.Marshal(map[string]any{
		"session_id":            "sess_1",
		"cache_scope":           "session",
		"cache_key_hash":        "sha256:abc",
		"hit":                   true,
		"estimated_savings_usd": 0.05,
		"avoided_tokens":        1024,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/cache-outcomes", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/cache-outcomes", nil)
	req.Header.Set("Authorization", "Bearer test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Outcomes []CacheOutcome `json:"outcomes"`
		Count    int            `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("expected 1 outcome, got %d", resp.Count)
	}
	if resp.Outcomes[0].ID == "" {
		t.Fatal("expected generated cache outcome ID")
	}
	if !resp.Outcomes[0].Hit {
		t.Fatal("expected cache hit")
	}
}

func TestRegistryHandler_CreateAndListEvidence(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestService(t, "sess_1", "local-dev")
	h := NewRegistryHandler(store, svc)

	body, _ := json.Marshal(map[string]any{
		"session_id":    "sess_1",
		"evidence_type": "test_result",
		"description":   "unit tests passed",
		"test_result":   "pass",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/evidence", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/evidence", nil)
	req.Header.Set("Authorization", "Bearer test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Evidence []EvidenceRecord `json:"evidence"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("expected 1 evidence, got %d", resp.Count)
	}
	if resp.Evidence[0].ID == "" {
		t.Fatal("expected generated evidence ID")
	}
	if resp.Evidence[0].EvidenceType != "test_result" {
		t.Fatalf("unexpected evidence type: %s", resp.Evidence[0].EvidenceType)
	}
}

func TestRegistryHandler_EvidenceRequiresSessionOwnership(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestService(t, "sess_other", "other-user")
	h := NewRegistryHandler(store, svc)

	body, _ := json.Marshal(map[string]any{
		"session_id":    "sess_other",
		"evidence_type": "test_result",
		"description":   "unit tests passed",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/evidence", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owned session, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRegistryHandler_EvidenceFailsClosedWithoutSessionStore(t *testing.T) {
	store := NewRegistryStore()
	svc := NewSessionService(SessionConfig{
		DevToken: "test",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	h := NewRegistryHandler(store, svc)

	body, _ := json.Marshal(map[string]any{
		"session_id":    "sess_1",
		"evidence_type": "test_result",
		"description":   "unit tests passed",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/evidence", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when session store is unavailable, got %d: %s", w.Code, w.Body.String())
	}
}

func registryTestService(t *testing.T, sessionID string, actor string) *SessionService {
	t.Helper()
	sessionStore, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	t.Cleanup(func() {
		_ = sessionStore.Close()
	})
	if err := sessionStore.Create(context.Background(), SessionRecord{
		SessionID:      sessionID,
		ActorSubject:   actor,
		Agent:          "test-generation",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "created",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return NewSessionService(SessionConfig{
		DevToken: "test",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: sessionStore,
	})
}
