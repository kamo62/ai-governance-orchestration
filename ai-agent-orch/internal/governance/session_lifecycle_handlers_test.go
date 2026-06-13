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

func TestAgentListHandlerReturnsCatalogAgents(t *testing.T) {
	root := filepath.Join("..", "..")
	handler := NewAgentListHandler(root)

	req := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result.Agents) == 0 {
		t.Fatalf("expected agents in response")
	}
	found := false
	for _, a := range result.Agents {
		if a.Name == "unit-tests" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unit-tests in agent list")
	}
}

func TestMessagesHandlerRequiresSessionID(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	orch := &fakeOrchestrator{}
	handler := NewMessagesHandler(service, orch)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions//messages", strings.NewReader(`{"prompt":"test"}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMessagesHandlerRoutesAndWritesAudit(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID:    fixedIDs("evt_route_1"),
	})
	orch := &fakeOrchestrator{specialist: "unit-tests", reason: "testing keyword match"}
	handler := NewMessagesHandler(service, orch)

	body := []byte(`{"prompt":"write tests for login"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["specialist"] != "unit-tests" {
		t.Fatalf("expected unit-tests, got %v", result["specialist"])
	}

	auditText := readAuditText(t, auditPath)
	if !strings.Contains(auditText, `"event_type":"router.specialist.selected"`) {
		t.Fatalf("expected router audit event: %s", auditText)
	}
}

func TestMessagesHandlerAdvancesSessionToAwaitingConfirmation(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer store.Close()
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      "sess_route_once",
		ActorSubject:   "local-dev",
		Agent:          "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "created",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: store,
		NewID:    fixedIDs("evt_route_state_1"),
	})
	handler := NewMessagesHandler(service, &fakeOrchestrator{specialist: "unit-tests", reason: "testing keyword match"})

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_route_once/messages", strings.NewReader(`{"prompt":"write tests"}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	record, err := store.Get(context.Background(), "sess_route_once")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if record.Status != "awaiting_confirmation" {
		t.Fatalf("expected awaiting_confirmation, got %q", record.Status)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_route_once/messages", strings.NewReader(`{"prompt":"route again"}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 on repeat routing, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMessagesHandlerBlocksSecretInFollowUp(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID:    fixedIDs("evt_secret_1"),
	})
	orch := &fakeOrchestrator{}
	handler := NewMessagesHandler(service, orch)

	fakeToken := "sk-or-v1-" + "1234567890"
	body := []byte(`{"prompt":"use OPENROUTER_API_KEY=` + fakeToken + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMessagesHandlerRejectsUnauthorizedBeforeReadingPrompt(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
	})
	orch := &fakeOrchestrator{}
	handler := NewMessagesHandler(service, orch)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/messages", strings.NewReader(`{"prompt":"raw prompt must not be read"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, "raw prompt") {
		t.Fatalf("unauthorized audit event must not contain raw prompt: %s", auditText)
	}
}

func TestConfirmHandlerRequiresAgent(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	orch := &fakeOrchestrator{}
	handler := NewConfirmHandlerWithEvents(service, orch, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/confirm", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConfirmHandlerBlocksWhenKillSwitchEnabled(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken:   "local-test-token",
		Audit:      audit.NewFileStore(auditPath),
		KillSwitch: true,
		NewID:      fixedIDs("evt_ks_1"),
	})
	orch := &fakeOrchestrator{}
	handler := NewConfirmHandlerWithEvents(service, orch, nil)

	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConfirmHandlerRequiresExplicitHumanConfirmationForLowConfidenceRoute(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer store.Close()

	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: store,
		NewID: fixedIDs(
			"run_low_confidence",
			"sess_low_confidence",
			"evt_session_low_confidence",
			"gateway_token_low_confidence",
			"evt_route_low_confidence",
		),
	})
	orch := &fakeOrchestrator{
		specialist:                "code-review",
		reason:                    "default route",
		routingConfidence:         "low",
		humanConfirmationRequired: true,
	}

	runReq := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(`{
		"agent":"governance-lead",
		"classification":"internal",
		"prompt":"please help"
	}`))
	runReq.Header.Set("Authorization", "Bearer local-test-token")
	runRec := httptest.NewRecorder()
	NewRunHandler(service, orch).ServeHTTP(runRec, runReq)
	if runRec.Code != http.StatusCreated {
		t.Fatalf("expected run creation 201, got %d: %s", runRec.Code, runRec.Body.String())
	}

	confirmReq := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_low_confidence/confirm", strings.NewReader(`{"agent":"code-review"}`))
	confirmReq.Header.Set("Authorization", "Bearer local-test-token")
	confirmRec := httptest.NewRecorder()
	NewConfirmHandlerWithEvents(service, orch, nil).ServeHTTP(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 requiring explicit human confirmation, got %d: %s", confirmRec.Code, confirmRec.Body.String())
	}
	if !strings.Contains(confirmRec.Body.String(), "human confirmation required") {
		t.Fatalf("expected human confirmation error, got %s", confirmRec.Body.String())
	}

	record, err := store.Get(context.Background(), "sess_low_confidence")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if record.Status != "awaiting_confirmation" {
		t.Fatalf("expected low-confidence session to remain awaiting_confirmation, got %q", record.Status)
	}
}

func TestConfirmHandlerAcceptsExplicitHumanConfirmationForLowConfidenceRoute(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer store.Close()
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:                 "sess_human_confirmed",
		ActorSubject:              "local-dev",
		Agent:                     "governance-lead",
		RoutedAgent:               "code-review",
		Classification:            "internal",
		PromptSHA256:              "abc123",
		Status:                    "awaiting_confirmation",
		RoutingConfidence:         "low",
		HumanConfirmationRequired: true,
		CreatedAt:                 time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: store,
		NewID:    fixedIDs("evt_human_confirm_1"),
	})
	handler := NewConfirmHandlerWithEvents(service, &fakeOrchestrator{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_human_confirmed/confirm", strings.NewReader(`{"agent":"code-review","human_confirmed":true}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	record, err := store.Get(context.Background(), "sess_human_confirmed")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if record.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %q", record.Status)
	}
}

func TestConfirmHandlerAcceptsAndWritesAudit(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID:    fixedIDs("evt_confirm_1"),
	})
	orch := &fakeOrchestrator{}
	handler := NewConfirmHandlerWithEvents(service, orch, nil)

	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	auditText := readAuditText(t, auditPath)
	if !strings.Contains(auditText, `"event_type":"session.confirmed"`) {
		t.Fatalf("expected confirmed audit event: %s", auditText)
	}
}

func TestConfirmHandlerRequiresAwaitingConfirmationState(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer store.Close()
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      "sess_not_routed",
		ActorSubject:   "local-dev",
		Agent:          "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "created",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: store,
	})
	handler := NewConfirmHandlerWithEvents(service, &fakeOrchestrator{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_not_routed/confirm", strings.NewReader(`{"agent":"unit-tests"}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConfirmHandlerConfirmsRoutedSession(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer store.Close()
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      "sess_routed",
		ActorSubject:   "local-dev",
		Agent:          "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "awaiting_confirmation",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: store,
		NewID:    fixedIDs("evt_confirm_state_1"),
	})
	handler := NewConfirmHandlerWithEvents(service, &fakeOrchestrator{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_routed/confirm", strings.NewReader(`{"agent":"unit-tests"}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	record, err := store.Get(context.Background(), "sess_routed")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if record.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %q", record.Status)
	}
}

func TestConfirmHandlerRejectsAgentMismatch(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer store.Close()
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      "sess_routed_mismatch",
		ActorSubject:   "local-dev",
		Agent:          "router-agent",
		RoutedAgent:    "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "awaiting_confirmation",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: store,
	})
	handler := NewConfirmHandlerWithEvents(service, &fakeOrchestrator{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_routed_mismatch/confirm", strings.NewReader(`{"agent":"code-review"}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConfirmHandlerWithEventsRequiresCachedPrompt(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	orch := &fakeOrchestrator{}
	handler := NewConfirmHandlerWithEvents(service, orch, NewEventStore())

	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_missing_prompt/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if orch.dispatchPrompt != "" {
		t.Fatalf("dispatch must not run without cached prompt, got %q", orch.dispatchPrompt)
	}
}

func TestConfirmHandlerDoesNotAdvanceStateWhenPromptMissing(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer store.Close()
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      "sess_missing_prompt",
		ActorSubject:   "local-dev",
		Agent:          "unit-tests",
		Classification: "internal",
		PromptSHA256:   "abc123",
		Status:         "awaiting_confirmation",
		CreatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: store,
	})
	handler := NewConfirmHandlerWithEvents(service, &fakeOrchestrator{}, NewEventStore())

	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_missing_prompt/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	record, err := store.Get(context.Background(), "sess_missing_prompt")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if record.Status != "awaiting_confirmation" {
		t.Fatalf("expected session to stay awaiting_confirmation, got %q", record.Status)
	}
}

func TestConfirmHandlerDispatchSurvivesRequestContextCancellation(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	service.rememberPrompt("sess_ctx", "write tests")
	orch := &contextProbeOrchestrator{
		dispatched: make(chan error, 1),
	}
	events := NewEventStore()
	handler := NewConfirmHandlerWithEvents(service, orch, events)
	stream := events.Subscribe("sess_ctx")

	ctx, cancel := context.WithCancel(context.Background())
	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_ctx/confirm", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	cancel()

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case err := <-orch.dispatched:
		if err != nil {
			t.Fatalf("dispatch should not inherit request cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatch")
	}

	// Drain until the executor closes the stream so its audit writes finish
	// before the test's TempDir is cleaned up.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, open := <-stream:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for executor to finish")
		}
	}
}

func TestPatchDecisionHandlerRequiresPatchID(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	handler := NewPatchDecisionHandler(service)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/patch-decision", strings.NewReader(`{"decision":"applied"}`))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchDecisionHandlerRecordsDecision(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID:    fixedIDs("evt_patch_1"),
	})
	handler := NewPatchDecisionHandler(service)
	service.rememberPatch("sess_123", "patch_1")

	body := []byte(`{"patch_id":"patch_1","decision":"applied"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/patch-decision", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	auditText := readAuditText(t, auditPath)
	if !strings.Contains(auditText, `"event_type":"patch.decision"`) {
		t.Fatalf("expected patch decision audit event: %s", auditText)
	}
}

func TestPatchDecisionHandlerRejectsUnknownPatchID(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	handler := NewPatchDecisionHandler(service)

	body := []byte(`{"patch_id":"missing_patch","decision":"applied"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/patch-decision", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchDecisionHandlerRejectsInvalidDecision(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	handler := NewPatchDecisionHandler(service)

	body := []byte(`{"patch_id":"patch_1","decision":"maybe"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/patch-decision", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminKillSwitchList(t *testing.T) {
	store := NewMemoryKillSwitch()
	store.Block("agent", "unit-tests")
	service := NewSessionService(SessionConfig{DevToken: "local-test-token", AdminToken: "admin-token"})
	handler := NewAdminHandler(store, service)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/killswitch", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result struct {
		KillSwitches map[string][]string `json:"killswitches"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result.KillSwitches["agent"]) != 1 {
		t.Fatalf("expected 1 blocked agent, got %v", result.KillSwitches)
	}
}

func TestAdminKillSwitchFailsClosedWhenAdminTokenEmpty(t *testing.T) {
	store := NewMemoryKillSwitch()
	service := NewSessionService(SessionConfig{DevToken: "local-test-token"})
	handler := NewAdminHandler(store, service)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/killswitch", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminKillSwitchBlockAndUnblock(t *testing.T) {
	store := NewMemoryKillSwitch()
	service := NewSessionService(SessionConfig{DevToken: "local-test-token", AdminToken: "admin-token"})
	handler := NewAdminHandler(store, service)

	// Block
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/killswitch/agent/unit-tests", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on block, got %d: %s", rec.Code, rec.Body.String())
	}
	if !store.IsBlocked("agent", "unit-tests") {
		t.Fatalf("expected agent to be blocked")
	}

	// Unblock
	req = httptest.NewRequest(http.MethodDelete, "/v1/admin/killswitch/agent/unit-tests", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on unblock, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.IsBlocked("agent", "unit-tests") {
		t.Fatalf("expected agent to be unblocked")
	}
}

func TestAgentKillSwitchBlocksSessionCreation(t *testing.T) {
	store := NewMemoryKillSwitch()
	if err := store.Block("agent", "unit-tests"); err != nil {
		t.Fatalf("block agent: %v", err)
	}
	service := NewSessionService(SessionConfig{
		DevToken:        "local-test-token",
		Audit:           audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		KillSwitchStore: store,
	})
	handler := NewSessionHandler(service)

	body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"write tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentKillSwitchBlocksConfirmation(t *testing.T) {
	store := NewMemoryKillSwitch()
	if err := store.Block("agent", "unit-tests"); err != nil {
		t.Fatalf("block agent: %v", err)
	}
	service := NewSessionService(SessionConfig{
		DevToken:        "local-test-token",
		Audit:           audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		KillSwitchStore: store,
	})
	orch := &fakeOrchestrator{}
	handler := NewConfirmHandlerWithEvents(service, orch, nil)

	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMetricsHandlerReturnsCounters(t *testing.T) {
	handler := NewMetricsHandler()
	handler.RecordSessionCreated()
	handler.RecordSessionDenied()
	handler.RecordSecretBlocked()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]uint64
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["sessions_created"] != 1 {
		t.Fatalf("expected sessions_created=1, got %d", result["sessions_created"])
	}
	if result["secrets_blocked"] != 1 {
		t.Fatalf("expected secrets_blocked=1, got %d", result["secrets_blocked"])
	}
}

func TestSessionServiceRecordsRealMetrics(t *testing.T) {
	metrics := NewMetricsHandler()
	service := NewSessionService(SessionConfig{
		DevToken:          "local-test-token",
		Audit:             audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		ClassificationMax: "internal",
		Metrics:           metrics,
	})
	handler := NewSessionHandler(service)

	body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"write tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	body = []byte(`{"agent":"unit-tests","classification":"restricted","prompt":"write tests"}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec = httptest.NewRecorder()
	metrics.ServeHTTP(rec, req)

	var result map[string]uint64
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["sessions_created"] != 1 {
		t.Fatalf("expected sessions_created=1, got %d", result["sessions_created"])
	}
	if result["sessions_denied"] != 1 {
		t.Fatalf("expected sessions_denied=1, got %d", result["sessions_denied"])
	}
	if result["classification_blocked"] != 1 {
		t.Fatalf("expected classification_blocked=1, got %d", result["classification_blocked"])
	}
}

func TestExtractSessionID(t *testing.T) {
	cases := []struct {
		path   string
		prefix string
		suffix string
		want   string
	}{
		{"/v1/sessions/sess_123/messages", "/v1/sessions/", "/messages", "sess_123"},
		{"/v1/sessions/sess_123/confirm", "/v1/sessions/", "/confirm", "sess_123"},
		{"/v1/sessions/sess_123/patch-decision", "/v1/sessions/", "/patch-decision", "sess_123"},
		{"/v1/sessions/", "/v1/sessions/", "/messages", ""},
		{"/other", "/v1/sessions/", "/messages", ""},
	}
	for _, c := range cases {
		got := extractSessionID(c.path, c.prefix, c.suffix)
		if got != c.want {
			t.Fatalf("extractSessionID(%q, %q, %q) = %q, want %q", c.path, c.prefix, c.suffix, got, c.want)
		}
	}
}

// fakeOrchestrator implements OrchestratorClient for tests.
type fakeOrchestrator struct {
	specialist                string
	reason                    string
	routingConfidence         string
	humanConfirmationRequired bool
	routingAlternates         []string
	err                       error
	acceptErr                 error
	dispatchErr               error
	dispatchPrompt            string
}

func (f *fakeOrchestrator) Route(ctx context.Context, sessionID string, prompt string, context SessionContext) (RouteDecision, error) {
	if f.err != nil {
		return RouteDecision{}, f.err
	}
	return RouteDecision{
		Specialist:                f.specialist,
		Reason:                    f.reason,
		RoutingConfidence:         f.routingConfidence,
		HumanConfirmationRequired: f.humanConfirmationRequired,
		RoutingAlternates:         f.routingAlternates,
	}, nil
}

func (f *fakeOrchestrator) AcceptSession(ctx context.Context, sessionID string, agent string) error {
	if f.acceptErr != nil {
		return f.acceptErr
	}
	return nil
}

func (f *fakeOrchestrator) Dispatch(ctx context.Context, sessionID string, agent string, prompt string, runtimeToken string) (DispatchResult, error) {
	if f.dispatchErr != nil {
		return DispatchResult{}, f.dispatchErr
	}
	if f.err != nil {
		return DispatchResult{}, f.err
	}
	f.dispatchPrompt = prompt
	return DispatchResult{
		SessionID: sessionID,
		Status:    "completed",
		Agent:     agent,
		Events: []DispatchEvent{
			{Type: "stream", Payload: "mock execution started"},
			{Type: "patch", Payload: `{"protocolVersion":1,"patchId":"patch_1","files":[{"path":"tests/example_test.go","action":"create","newContent":"package tests\n"}]}`},
			{Type: "done", Payload: "mock execution complete"},
		},
	}, nil
}

type contextProbeOrchestrator struct {
	dispatched chan error
}

func (o *contextProbeOrchestrator) Route(ctx context.Context, sessionID string, prompt string, context SessionContext) (RouteDecision, error) {
	return RouteDecision{Specialist: "unit-tests", Reason: "test"}, nil
}

func (o *contextProbeOrchestrator) AcceptSession(ctx context.Context, sessionID string, agent string) error {
	return nil
}

func (o *contextProbeOrchestrator) Dispatch(ctx context.Context, sessionID string, agent string, prompt string, runtimeToken string) (DispatchResult, error) {
	select {
	case <-ctx.Done():
		o.dispatched <- ctx.Err()
		return DispatchResult{}, ctx.Err()
	case <-time.After(50 * time.Millisecond):
		o.dispatched <- nil
		return DispatchResult{
			SessionID: sessionID,
			Status:    "completed",
			Agent:     agent,
			Events: []DispatchEvent{
				{Type: "patch", Payload: `{"protocolVersion":1,"patchId":"patch_ctx","files":[{"path":"tests/context_test.go","action":"create","newContent":"package tests\n"}]}`},
				{Type: "done", Payload: "ok"},
			},
		}, nil
	}
}
