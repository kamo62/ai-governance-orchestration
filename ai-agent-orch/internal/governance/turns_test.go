package governance

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestTurnsHandlerFollowUpOnDoneSession(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    auditStore,
		Sessions: store,
		NewID:    fixedIDs("evt_turn_1", "evt_turn_2", "evt_turn_3", "evt_turn_4"),
	})
	events := NewEventStore()
	orch := &fakeOrchestrator{specialist: "unit-tests", reason: "follow-up routing"}
	handler := NewTurnsHandler(service, orch, events)

	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      "sess_turn_1",
		ActorSubject:   "local-dev",
		Agent:          "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc",
		Status:         "done",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	body := []byte(`{"prompt":"add another test","auto_confirm":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_turn_1/turns", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("X-AI-Orch-Client", "ai-agent-bridge")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"turn":true`) {
		t.Fatalf("expected turn response, got %s", rec.Body.String())
	}
	if _, ok := service.promptForSession("sess_turn_1"); ok {
		t.Fatal("expected auto-confirm follow-up prompt to be cleared from local memory")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		record, err := store.Get(context.Background(), "sess_turn_1")
		if err == nil && (record.Status == "running" || record.Status == "done") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	record, _ := store.Get(context.Background(), "sess_turn_1")
	t.Fatalf("expected session to reach running/done, got %q", record.Status)
}

func TestTurnsHandlerRejectsActiveSession(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: store,
	})
	handler := NewTurnsHandler(service, &fakeOrchestrator{}, NewEventStore())
	_ = store.Create(context.Background(), SessionRecord{
		SessionID:      "sess_active",
		ActorSubject:   "local-dev",
		Agent:          "unit-tests",
		Classification: "internal",
		Status:         "running",
		CreatedAt:      time.Now().UTC(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_active/turns", strings.NewReader(`{"prompt":"x","auto_confirm":true}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}
