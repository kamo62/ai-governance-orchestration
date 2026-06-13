package governance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestEventStoreBoundsClosedSessionHistory(t *testing.T) {
	store := NewEventStore()

	for i := 0; i < maxClosedSessionHistory+20; i++ {
		sessionID := fmt.Sprintf("sess_%03d", i)
		store.Publish(sessionID, SessionEvent{Type: "stream", Payload: "event"})
		store.Close(sessionID)
	}

	store.mu.RLock()
	defer store.mu.RUnlock()

	if len(store.history) > maxClosedSessionHistory {
		t.Fatalf("expected at most %d history entries, got %d", maxClosedSessionHistory, len(store.history))
	}
	if len(store.closed) > maxClosedSessionHistory {
		t.Fatalf("expected at most %d closed entries, got %d", maxClosedSessionHistory, len(store.closed))
	}
}

func TestEventsHandlerEnforcesSessionOwnership(t *testing.T) {
	store, err := NewSQLiteSessionStore(":memory:")
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer store.Close()
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      "sess_owner",
		ActorSubject:   "owner-dev",
		Agent:          "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "running",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(""),
		Sessions: store,
	})
	handler := NewEventsHandler(NewEventStore(), service)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_owner/events", nil)
	req = req.WithContext(WithAuthInfo(req.Context(), AuthInfo{Subject: "other-dev", Method: "dev"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
