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

	"ai-agent-orch/internal/audit"
)

func TestAdminAuditHandler_RetentionNotSupportedForFileStore(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	store := audit.NewFileStore(auditPath)
	handler := NewAdminAuditHandler(store, adminAuditService(store))

	body, _ := json.Marshal(map[string]any{"max_age_hours": 24})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/audit/retention", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 for file store, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminAuditHandler_RequiresAuthorization(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	store := audit.NewFileStore(auditPath)
	handler := NewAdminAuditHandler(store, adminAuditService(store))

	body, _ := json.Marshal(map[string]any{"max_age_hours": 24})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/audit/retention", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminAuditHandler_RetentionPurgesOldEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	store, err := audit.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create sqlite store: %v", err)
	}
	defer store.Close()

	// Insert an old event and a recent event.
	oldEvent := audit.Event{
		EventID:    "evt_old",
		SessionID:  "sess_retention",
		EventType:  "test.old",
		RecordedAt: time.Now().UTC().Add(-48 * time.Hour),
	}
	_, err = store.Append(context.Background(), oldEvent)
	if err != nil {
		t.Fatalf("append old event: %v", err)
	}

	recentEvent := audit.Event{
		EventID:    "evt_recent",
		SessionID:  "sess_retention",
		EventType:  "test.recent",
		RecordedAt: time.Now().UTC(),
	}
	_, err = store.Append(context.Background(), recentEvent)
	if err != nil {
		t.Fatalf("append recent event: %v", err)
	}

	handler := NewAdminAuditHandler(store, adminAuditService(store))
	body, _ := json.Marshal(map[string]any{"max_age_hours": 24})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/audit/retention", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"purged":1`) {
		t.Fatalf("expected 1 purged event: %s", rec.Body.String())
	}

	// Verify only recent event remains.
	events, err := store.EventsBySession(context.Background(), "sess_retention")
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 remaining event, got %d", len(events))
	}
	if events[0].EventID != "evt_recent" {
		t.Fatalf("expected evt_recent, got %s", events[0].EventID)
	}
}

func adminAuditService(store audit.Store) *SessionService {
	return NewSessionService(SessionConfig{
		DevToken: "test-token",
		Audit:    store,
	})
}
