package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestRegistryHandler_CreateAndListCacheOutcome(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestServiceWithSessions(t, map[string]string{
		"sess_1":     "local-dev",
		"sess_other": "other-user",
	})
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

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
	if err := store.AppendCacheOutcome(CacheOutcome{
		ID:           "cache_other",
		SessionID:    "sess_other",
		CacheScope:   "session",
		CacheKeyHash: "sha256:other",
		RecordedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append other cache outcome: %v", err)
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
	if resp.Outcomes[0].SessionID != "sess_1" {
		t.Fatalf("unexpected visible session: %s", resp.Outcomes[0].SessionID)
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
	svc := registryTestServiceWithSessions(t, map[string]string{
		"sess_1":     "local-dev",
		"sess_other": "other-user",
	})
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

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
	if _, _, err := store.AppendEvidence(EvidenceRecord{
		ID:           "evidence_other",
		SessionID:    "sess_other",
		EvidenceType: "test_result",
		Description:  "other user's tests",
		RecordedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append other evidence: %v", err)
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
	if resp.Evidence[0].SessionID != "sess_1" {
		t.Fatalf("unexpected visible session: %s", resp.Evidence[0].SessionID)
	}
	if resp.Evidence[0].ID == "" {
		t.Fatal("expected generated evidence ID")
	}
	if resp.Evidence[0].EvidenceType != "test_result" {
		t.Fatalf("unexpected evidence type: %s", resp.Evidence[0].EvidenceType)
	}
	if resp.Evidence[0].TrustLevel != "self_reported" || resp.Evidence[0].EnforcementMode != "advisory" {
		t.Fatalf("expected ordinary evidence to be labelled self_reported/advisory, got %#v", resp.Evidence[0])
	}
}

func TestRegistryHandler_CreateEvidenceRejectsOversizedBody(t *testing.T) {
	h := NewRegistryHandlerWithMetrics(NewRegistryStore(), registryTestService(t, "sess_1", "local-dev"), nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/evidence", strings.NewReader(`{"description":"`+strings.Repeat("x", maxRequestBodyBytes)+`"}`))
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestRegistryHandler_CreateEvidenceDedupesClientEventID verifies POST
// /v1/evidence is safe to retry: replaying the same client_event_id returns
// the normal 201 success shape with a duplicate marker instead of an error,
// and does not create a second stored record.
func TestRegistryHandler_CreateEvidenceDedupesClientEventID(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestService(t, "sess_1", "local-dev")
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

	post := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{
			"session_id":      "sess_1",
			"evidence_type":   "test_result",
			"description":     "unit tests passed",
			"client_event_id": "cev_retry_1",
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/evidence", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	first := post()
	if first.Code != http.StatusCreated {
		t.Fatalf("expected 201 on first post, got %d: %s", first.Code, first.Body.String())
	}
	var firstResp struct {
		EvidenceRecord
		Duplicate bool `json:"duplicate"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if firstResp.Duplicate {
		t.Fatal("expected the first post to not be marked duplicate")
	}
	if firstResp.ID == "" {
		t.Fatal("expected a generated evidence ID")
	}

	retry := post()
	if retry.Code != http.StatusCreated {
		t.Fatalf("expected 201 (normal success shape) on retried post, got %d: %s", retry.Code, retry.Body.String())
	}
	var retryResp struct {
		EvidenceRecord
		Duplicate bool `json:"duplicate"`
	}
	if err := json.Unmarshal(retry.Body.Bytes(), &retryResp); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if !retryResp.Duplicate {
		t.Fatal("expected the retried post to be marked duplicate")
	}
	if retryResp.ID != firstResp.ID {
		t.Fatalf("expected the duplicate response to reference the original record %q, got %q", firstResp.ID, retryResp.ID)
	}

	ev, err := store.Evidence()
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(ev) != 1 {
		t.Fatalf("expected exactly 1 stored record after the retry, got %d", len(ev))
	}
}

func TestRegistryHandler_DerivesExternalEvidenceTrustFields(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestService(t, "sess_1", "local-dev")
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

	body, _ := json.Marshal(map[string]any{
		"session_id":       "sess_1",
		"evidence_type":    "external_tool_call",
		"description":      "native client reported a shell command",
		"trust_level":      "gateway_enforced",
		"enforcement_mode": "gateway",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/evidence", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("X-AI-Orch-Trust-Level", "gateway_enforced")
	req.Header.Set("X-AI-Orch-Enforcement-Mode", "gateway")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var got EvidenceRecord
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	if got.TrustLevel != "self_reported" || got.EnforcementMode != "advisory" {
		t.Fatalf("external evidence must remain self_reported/advisory, got %#v", got)
	}
	if got.SourceAuthority != 4 || got.EvidenceStrength != "weak" || got.Status != "proposed" {
		t.Fatalf("external evidence must use self-reported provenance, got %#v", got)
	}
}

func TestRegistryHandler_DerivesEvidenceProvenanceByLane(t *testing.T) {
	tests := []struct {
		name          string
		client        string
		strength      string
		wantTrust     string
		wantAuthority int
		wantStrength  string
		wantStatus    string
	}{
		{name: "gateway defaults", client: "ai-orch-mcp", wantTrust: "gateway_enforced", wantAuthority: 1, wantStrength: "strong", wantStatus: "candidate"},
		{name: "gateway accepts below cap", client: "ai-orch-mcp", strength: "medium", wantTrust: "gateway_enforced", wantAuthority: 1, wantStrength: "medium", wantStatus: "candidate"},
		{name: "managed defaults", client: "ai-agent-bridge", wantTrust: "managed_client", wantAuthority: 3, wantStrength: "medium", wantStatus: "candidate"},
		{name: "self reported defaults", client: "unknown", wantTrust: "self_reported", wantAuthority: 4, wantStrength: "weak", wantStatus: "proposed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewRegistryStore()
			svc := registryTestService(t, "sess_1", "local-dev")
			svc.trustedClientToken = "trusted"
			h := NewRegistryHandlerWithMetrics(store, svc, nil)
			body := map[string]any{
				"session_id":    "sess_1",
				"evidence_type": "test_result",
				"description":   "unit tests passed",
				"subject_key":   "repo:example",
				"confidence":    0.9,
			}
			if test.strength != "" {
				body["evidence_strength"] = test.strength
			}
			encoded, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/v1/evidence", bytes.NewReader(encoded))
			req.Header.Set("Authorization", "Bearer test")
			req.Header.Set("X-AI-Orch-Client", test.client)
			req.Header.Set("X-AI-Orch-Trusted-Client-Token", "trusted")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
			}
			var got EvidenceRecord
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode evidence: %v", err)
			}
			if got.TrustLevel != test.wantTrust || got.SourceAuthority != test.wantAuthority || got.EvidenceStrength != test.wantStrength || got.Status != test.wantStatus {
				t.Fatalf("unexpected provenance: %#v", got)
			}
			if got.SubjectKey != "repo:example" || got.Confidence == nil || *got.Confidence != 0.9 || got.ConfidenceBand != "high" {
				t.Fatalf("unexpected subject/confidence response: %#v", got)
			}
		})
	}
}

func TestRegistryHandler_RejectsEvidenceProvenanceOverclaims(t *testing.T) {
	tests := []struct {
		name   string
		client string
		field  string
		value  any
	}{
		{name: "confirmed status", client: "unknown", field: "status", value: "confirmed"},
		{name: "human authority", client: "ai-orch-mcp", field: "source_authority", value: 2},
		{name: "authority escalation", client: "ai-agent-bridge", field: "source_authority", value: 1},
		{name: "strength above cap", client: "ai-agent-bridge", field: "evidence_strength", value: "strong"},
		{name: "confidence out of range", client: "unknown", field: "confidence", value: 1.1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewRegistryStore()
			svc := registryTestService(t, "sess_1", "local-dev")
			svc.trustedClientToken = "trusted"
			h := NewRegistryHandlerWithMetrics(store, svc, nil)
			body := map[string]any{
				"session_id":    "sess_1",
				"evidence_type": "test_result",
				"description":   "claim",
				test.field:      test.value,
			}
			encoded, _ := json.Marshal(body)
			req := httptest.NewRequest(http.MethodPost, "/v1/evidence", bytes.NewReader(encoded))
			req.Header.Set("Authorization", "Bearer test")
			req.Header.Set("X-AI-Orch-Client", test.client)
			req.Header.Set("X-AI-Orch-Trusted-Client-Token", "trusted")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), test.field) {
				t.Fatalf("expected 400 naming %s, got %d: %s", test.field, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRegistryHandler_EvidenceRequiresSessionOwnership(t *testing.T) {
	store := NewRegistryStore()
	svc := registryTestService(t, "sess_other", "other-user")
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

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
	h := NewRegistryHandlerWithMetrics(store, svc, nil)

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
	return registryTestServiceWithSessions(t, map[string]string{sessionID: actor})
}

func registryTestServiceWithSessions(t *testing.T, sessions map[string]string) *SessionService {
	t.Helper()
	sessionStore, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	t.Cleanup(func() {
		_ = sessionStore.Close()
	})
	for sessionID, actor := range sessions {
		if err := sessionStore.Create(context.Background(), SessionRecord{
			SessionID:      sessionID,
			ActorSubject:   actor,
			Agent:          "unit-tests",
			Classification: "internal",
			PromptSHA256:   "abc123",
			Status:         "created",
			CreatedAt:      time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create session %s: %v", sessionID, err)
		}
	}
	return NewSessionService(SessionConfig{
		DevToken: "test",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: sessionStore,
	})
}
