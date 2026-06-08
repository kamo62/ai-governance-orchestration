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
	"ai-agent-orch/internal/composition"
)

type noopAudit struct{}

func (n *noopAudit) Append(_ context.Context, ev audit.Event) (audit.Event, error) {
	return ev, nil
}
func (n *noopAudit) EventsBySession(_ context.Context, _ string) ([]audit.Event, error) {
	return nil, nil
}

func TestCompositionHandler_CreateAndFlow(t *testing.T) {
	store := composition.NewCompositionStore()
	sessionStore := compositionSessionStore(t, "sess_test", "local-dev")
	service := NewSessionService(SessionConfig{
		DevToken: "test-token",
		Audit:    &noopAudit{},
		Sessions: sessionStore,
	})
	handler := NewCompositionHandler(service, store)

	// Create composition.
	createBody, _ := json.Marshal(map[string]any{
		"session_id":  "sess_test",
		"description": "test composition",
		"stages": []map[string]string{
			{"name": "investigate", "agent": "security-scan"},
			{"name": "plan", "agent": "architecture-review"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/compositions", bytes.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"created"`) {
		t.Fatalf("expected created status: %s", rec.Body.String())
	}

	// Get composition.
	req = httptest.NewRequest(http.MethodGet, "/v1/compositions/sess_test", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"current_idx":0`) {
		t.Fatalf("expected current_idx 0: %s", rec.Body.String())
	}

	// Complete stage 0.
	completeBody, _ := json.Marshal(map[string]any{"output": "investigation done"})
	req = httptest.NewRequest(http.MethodPost, "/v1/compositions/sess_test/complete", bytes.NewReader(completeBody))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on complete, got %d: %s", rec.Code, rec.Body.String())
	}

	// Approve stage 0.
	req = httptest.NewRequest(http.MethodPost, "/v1/compositions/sess_test/approve", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on approve, got %d: %s", rec.Code, rec.Body.String())
	}

	// Advance to stage 1.
	req = httptest.NewRequest(http.MethodPost, "/v1/compositions/sess_test/advance", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on advance, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"current_idx":1`) {
		t.Fatalf("expected current_idx 1: %s", rec.Body.String())
	}

	// Advance past max depth should fail.
	req = httptest.NewRequest(http.MethodPost, "/v1/compositions/sess_test/complete", bytes.NewReader(completeBody))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	req = httptest.NewRequest(http.MethodPost, "/v1/compositions/sess_test/approve", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	req = httptest.NewRequest(http.MethodPost, "/v1/compositions/sess_test/advance", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 max depth exceeded, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCompositionHandler_RejectUnapprovedAdvance(t *testing.T) {
	store := composition.NewCompositionStore()
	sessionStore := compositionSessionStore(t, "sess_test", "local-dev")
	service := NewSessionService(SessionConfig{
		DevToken: "test-token",
		Audit:    &noopAudit{},
		Sessions: sessionStore,
	})
	handler := NewCompositionHandler(service, store)

	createBody, _ := json.Marshal(map[string]any{
		"session_id": "sess_test",
		"stages":     []map[string]string{{"name": "s1", "agent": "a1"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/compositions", bytes.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Complete stage but don't approve — advance should fail with human gate.
	completeBody, _ := json.Marshal(map[string]any{"output": "done"})
	req = httptest.NewRequest(http.MethodPost, "/v1/compositions/sess_test/complete", bytes.NewReader(completeBody))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodPost, "/v1/compositions/sess_test/advance", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected 423 human gate required, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCompositionHandler_EnforcesSessionOwnershipForLifecycleCalls(t *testing.T) {
	store := composition.NewCompositionStore()
	sessionStore := compositionSessionStore(t, "sess_test", "owner-a")
	store.Set("sess_test", composition.NewComposition("sess_test", []composition.Stage{
		{Name: "investigate", Agent: "security-scan"},
	}))
	service := NewSessionService(SessionConfig{
		Authorizer: fixedSubjectAuthorizer{subject: "owner-b"},
		Audit:      &noopAudit{},
		Sessions:   sessionStore,
	})
	handler := NewCompositionHandler(service, store)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "get", method: http.MethodGet, path: "/v1/compositions/sess_test"},
		{name: "complete", method: http.MethodPost, path: "/v1/compositions/sess_test/complete", body: `{"output":"done"}`},
		{name: "approve", method: http.MethodPost, path: "/v1/compositions/sess_test/approve"},
		{name: "advance", method: http.MethodPost, path: "/v1/compositions/sess_test/advance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 ownership mismatch, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func compositionSessionStore(t *testing.T, sessionID string, actor string) *SQLiteSessionStore {
	t.Helper()
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      sessionID,
		ActorSubject:   actor,
		Agent:          "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "created",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return store
}

type fixedSubjectAuthorizer struct {
	subject string
}

func (f fixedSubjectAuthorizer) Validate(context.Context, string) (string, bool) {
	return f.subject, f.subject != ""
}
