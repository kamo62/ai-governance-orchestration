package governance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestAdminRegistryHandlerListsCrossActorEvidence(t *testing.T) {
	store := NewRegistryStore()
	if err := store.AppendEvidence(EvidenceRecord{ID: "ev_1", SessionID: "sess_1", EvidenceType: "test", Description: "owned", RecordedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("append evidence: %v", err)
	}
	if err := store.AppendEvidence(EvidenceRecord{ID: "ev_2", SessionID: "sess_2", EvidenceType: "review", Description: "other", RecordedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("append evidence: %v", err)
	}

	h := NewAdminRegistryHandler(store, adminRegistryService(t))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/evidence", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Evidence []EvidenceRecord `json:"evidence"`
		Count    int              `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Count != 2 || len(resp.Evidence) != 2 {
		t.Fatalf("expected both actors' evidence, got %#v", resp)
	}
}

func TestAdminRegistryHandlerListsCrossActorMaturityAndCache(t *testing.T) {
	store := NewRegistryStore()
	if err := store.AppendExport(MaturityExportRecord{SessionID: "sess_1", EventType: "session.summary", Actor: "local-dev", RecordedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("append export: %v", err)
	}
	if err := store.AppendExport(MaturityExportRecord{SessionID: "sess_2", EventType: "session.summary", Actor: "other", RecordedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("append export: %v", err)
	}
	if err := store.AppendCacheOutcome(CacheOutcome{ID: "cache_1", SessionID: "sess_1", CacheScope: "context", CacheKeyHash: "sha256:1", Hit: true, RecordedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("append cache: %v", err)
	}
	if err := store.AppendCacheOutcome(CacheOutcome{ID: "cache_2", SessionID: "sess_2", CacheScope: "context", CacheKeyHash: "sha256:2", Hit: false, RecordedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("append cache: %v", err)
	}

	h := NewAdminRegistryHandler(store, adminRegistryService(t))
	for _, tc := range []struct {
		path string
		key  string
	}{
		{path: "/v1/admin/reporting/maturity-governance", key: "exports"},
		{path: "/v1/admin/cache-outcomes", key: "outcomes"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer admin-token")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if int(resp["count"].(float64)) != 2 {
				t.Fatalf("expected count 2, got %v", resp["count"])
			}
			if _, ok := resp[tc.key]; !ok {
				t.Fatalf("expected %s in response: %#v", tc.key, resp)
			}
		})
	}
}

func TestAdminRegistryHandlerRequiresAdminToken(t *testing.T) {
	h := NewAdminRegistryHandler(NewRegistryStore(), adminRegistryService(t))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/evidence", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func adminRegistryService(t *testing.T) *SessionService {
	t.Helper()
	return NewSessionService(SessionConfig{
		DevToken:   "dev-token",
		AdminToken: "admin-token",
		Audit:      audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
}
