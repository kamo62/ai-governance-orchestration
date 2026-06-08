package governance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-agent-orch/internal/audit"
)

// TestAllBoundariesFailClosedUnderFailure proves every governance boundary
// behaves correctly when its dependency fails.
func TestAllBoundariesFailClosedUnderFailure(t *testing.T) {
	// Subtests run in sequence so audit file state is predictable.
	t.Run("audit_write_failure_blocks_session_creation", testAuditFailureBlocksCreate)
	t.Run("audit_write_failure_blocks_routing", testAuditFailureBlocksRouting)
	t.Run("audit_write_failure_blocks_confirmation", testAuditFailureBlocksConfirm)
	t.Run("audit_write_failure_blocks_abort", testAuditFailureBlocksAbort)
	t.Run("audit_write_failure_blocks_patch_decision", testAuditFailureBlocksPatch)
	t.Run("orchestrator_unreachable_returns_502", testOrchestratorUnreachable)
	t.Run("orchestrator_dispatch_failure_streams_error", testDispatchFailure)
	t.Run("unauthorized_request_rejected_on_all_paths", testUnauthorizedAllPaths)
	t.Run("kill_switch_blocks_before_any_processing", testKillSwitchBlocksEarly)
	t.Run("secret_detection_blocks_before_model_call", testSecretBlocksBeforeModel)
	t.Run("classification_ceiling_blocks_before_model", testClassificationBlocks)
	t.Run("cost_cap_blocks_before_model", testCostCapBlocks)
	t.Run("raw_prompt_never_appears_in_response_or_audit", testRawPromptNeverLeaks)
	t.Run("raw_prompt_never_appears_in_sse_events", testRawPromptNeverInSSE)
	t.Run("request_context_cancellation_does_not_cancel_stream", testRequestCancellationDoesNotCancelStream)
}

func testAuditFailureBlocksCreate(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    failingAuditStore{},
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"normal prompt"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sess_") {
		t.Fatal("session ID must not leak when audit fails")
	}
}

func testAuditFailureBlocksRouting(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    failingAuditStore{},
	})
	orch := &fakeOrchestrator{specialist: "unit-tests", reason: "test"}
	handler := NewMessagesHandler(service, orch)

	body := []byte(`{"prompt":"write tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "unit-tests") {
		t.Fatal("specialist must not leak when audit fails")
	}
}

func testAuditFailureBlocksConfirm(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    failingAuditStore{},
	})
	orch := &fakeOrchestrator{}
	handler := NewConfirmHandlerWithEvents(service, orch, nil)

	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when confirmation audit fails, got %d: %s", rec.Code, rec.Body.String())
	}
}

func testAuditFailureBlocksAbort(t *testing.T) {
	store, err := NewSQLiteSessionStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	defer store.Close()
	if err := store.Create(context.Background(), SessionRecord{
		SessionID:      "sess_abort",
		ActorSubject:   "local-dev",
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
		Audit:    failingAuditStore{},
		Sessions: store,
	})
	handler := NewAbortHandler(service, NewEventStore())

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_abort/abort", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	record, err := store.Get(context.Background(), "sess_abort")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if record.Status != "running" {
		t.Fatalf("abort must not mutate state when audit fails, got %q", record.Status)
	}
}

func testAuditFailureBlocksPatch(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    failingAuditStore{},
	})
	service.rememberPatch("sess_123", "p1")
	handler := NewPatchDecisionHandler(service)

	body := []byte(`{"patch_id":"p1","decision":"applied"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/patch-decision", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func testOrchestratorUnreachable(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	orch := &failingOrchestrator{err: errors.New("connection refused")}
	handler := NewMessagesHandler(service, orch)

	body := []byte(`{"prompt":"write tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

func testDispatchFailure(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
	})
	orch := &fakeOrchestrator{err: errors.New("dispatch failed")}
	events := NewEventStore()
	service.rememberPrompt("sess_123", "write tests")
	handler := NewConfirmHandlerWithEvents(service, orch, events)

	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("confirm should succeed, got %d: %s", rec.Code, rec.Body.String())
	}

	// Wait for background execution.
	time.Sleep(100 * time.Millisecond)

	ch := events.Subscribe("sess_123")
	defer events.Unsubscribe("sess_123", ch)

	var foundError bool
	done := time.After(2 * time.Second)
loop:
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				break loop
			}
			if event.Type == "error" {
				foundError = true
				break loop
			}
			if event.Type == "done" {
				break loop
			}
		case <-done:
			break loop
		}
	}

	if !foundError {
		t.Fatal("expected error event when dispatch fails")
	}
}

func testUnauthorizedAllPaths(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
	})
	orch := &fakeOrchestrator{}
	events := NewEventStore()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create_session", http.MethodPost, "/v1/sessions", `{"agent":"test","classification":"internal","prompt":"x"}`},
		{"send_message", http.MethodPost, "/v1/sessions/sess_123/messages", `{"prompt":"x"}`},
		{"confirm", http.MethodPost, "/v1/sessions/sess_123/confirm", `{"agent":"test"}`},
		{"patch_decision", http.MethodPost, "/v1/sessions/sess_123/patch-decision", `{"patch_id":"p1","decision":"applied"}`},
		{"audit_lookup", http.MethodGet, "/v1/audit/sessions/sess_123", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			if tt.body != "" {
				body = []byte(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer wrong-token")
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			switch {
			case tt.path == "/v1/sessions":
				NewSessionHandler(service).ServeHTTP(rec, req)
			case strings.HasSuffix(tt.path, "/messages"):
				NewMessagesHandler(service, orch).ServeHTTP(rec, req)
			case strings.HasSuffix(tt.path, "/confirm"):
				NewConfirmHandlerWithEvents(service, orch, events).ServeHTTP(rec, req)
			case strings.HasSuffix(tt.path, "/patch-decision"):
				NewPatchDecisionHandler(service).ServeHTTP(rec, req)
			case strings.HasPrefix(tt.path, "/v1/audit/sessions/"):
				NewAuditLookupHandler(AuditLookupConfig{DevToken: "local-test-token", Audit: audit.NewFileStore(auditPath)}).ServeHTTP(rec, req)
			}

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "x") || strings.Contains(rec.Body.String(), "test") {
				t.Fatal("response must not echo request content on auth failure")
			}
		})
	}
}

func testKillSwitchBlocksEarly(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken:   "local-test-token",
		Audit:      audit.NewFileStore(auditPath),
		KillSwitch: true,
		NewID:      fixedIDs("evt_ks_1"),
	})
	handler := NewSessionHandler(service)

	body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"this prompt must not be processed"}`)
	req := authorizedSessionRequest(string(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d: %s", rec.Code, rec.Body.String())
	}

	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, "this prompt must not be processed") {
		t.Fatal("kill-switch audit must not include raw prompt")
	}
	if !strings.Contains(auditText, `"reason":"kill switch enabled"`) {
		t.Fatalf("expected kill switch audit: %s", auditText)
	}
}

func testSecretBlocksBeforeModel(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID:    fixedIDs("evt_secret_1"),
	})
	handler := NewSessionHandler(service)

	fakeKey := "sk-or-v1-" + "test1234567890"
	body := fmt.Sprintf(`{"agent":"unit-tests","classification":"internal","prompt":"use key=%s"}`, fakeKey)
	req := authorizedSessionRequest(body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, fakeKey) {
		t.Fatal("secret-denied audit must not include raw secret")
	}
	if !strings.Contains(auditText, `"findings":["openrouter_api_key"]`) {
		t.Fatalf("expected secret finding in audit: %s", auditText)
	}
}

func testClassificationBlocks(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken:          "local-test-token",
		Audit:             audit.NewFileStore(auditPath),
		ClassificationMax: "internal",
		NewID:             fixedIDs("evt_class_1"),
	})
	handler := NewSessionHandler(service)

	body := `{"agent":"unit-tests","classification":"restricted","prompt":"restricted content"}`
	req := authorizedSessionRequest(body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, "restricted content") {
		t.Fatal("classification-denied audit must not include raw prompt")
	}
}

func testCostCapBlocks(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken:          "local-test-token",
		Audit:             audit.NewFileStore(auditPath),
		CostCapEnabled:    true,
		SessionCostCapUSD: 0.25,
		NewID:             fixedIDs("evt_cost_1"),
	})
	handler := NewSessionHandler(service)

	body := `{"agent":"unit-tests","classification":"internal","prompt":"expensive prompt","estimated_cost_usd":0.50}`
	req := authorizedSessionRequest(body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d: %s", rec.Code, rec.Body.String())
	}

	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, "expensive prompt") {
		t.Fatal("cost-denied audit must not include raw prompt")
	}
	if !strings.Contains(auditText, `"reason":"cost cap exceeded"`) {
		t.Fatalf("expected cost denial audit: %s", auditText)
	}
}

func testRawPromptNeverLeaks(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID:    fixedIDs("sess_safe_1", "evt_safe_1"),
	})
	handler := NewSessionHandler(service)

	prompt := "my super secret business logic about payment processing"
	body := fmt.Sprintf(`{"agent":"unit-tests","classification":"internal","prompt":"%s"}`, prompt)
	req := authorizedSessionRequest(body)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Response must not contain raw prompt.
	if strings.Contains(rec.Body.String(), prompt) {
		t.Fatalf("HTTP response must not contain raw prompt: %s", rec.Body.String())
	}

	// Audit must not contain raw prompt.
	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, prompt) {
		t.Fatalf("audit file must not contain raw prompt: %s", auditText)
	}

	// But audit must contain the hash.
	expectedHash := sha256.Sum256([]byte(prompt))
	if !strings.Contains(auditText, hex.EncodeToString(expectedHash[:])) {
		t.Fatalf("audit must contain prompt hash: %s", auditText)
	}
}

func testRawPromptNeverInSSE(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	orch := &fakeOrchestrator{}
	events := NewEventStore()
	service.rememberPrompt("sess_sse", "secret prompt")
	handler := NewConfirmHandlerWithEvents(service, orch, events)

	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_sse/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("confirm expected 200, got %d", rec.Code)
	}

	time.Sleep(100 * time.Millisecond)

	ch := events.Subscribe("sess_sse")
	defer events.Unsubscribe("sess_sse", ch)

	done := time.After(2 * time.Second)
loop:
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				break loop
			}
			if strings.Contains(event.Payload, "secret prompt") {
				t.Fatalf("SSE event must not contain raw prompt: %+v", event)
			}
			if event.Type == "done" {
				break loop
			}
		case <-done:
			break loop
		}
	}
}

func testRequestCancellationDoesNotCancelStream(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	orch := &slowOrchestrator{delay: 50 * time.Millisecond}
	events := NewEventStore()
	service.rememberPrompt("sess_timeout", "slow prompt")
	handler := NewConfirmHandlerWithEvents(service, orch, events)

	body := []byte(`{"agent":"unit-tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_timeout/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	cancel()

	if rec.Code != http.StatusOK {
		t.Fatalf("confirm expected 200, got %d", rec.Code)
	}

	time.Sleep(150 * time.Millisecond)

	ch := events.Subscribe("sess_timeout")
	defer events.Unsubscribe("sess_timeout", ch)

	var foundDone bool
	done := time.After(2 * time.Second)
loop:
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				break loop
			}
			if event.Type == "done" || event.Type == "error" {
				foundDone = true
				break loop
			}
		case <-done:
			break loop
		}
	}

	if !foundDone {
		t.Fatal("expected stream to complete after request context cancellation")
	}
}

// failingOrchestrator always fails.
type failingOrchestrator struct {
	err error
}

func (f *failingOrchestrator) Route(ctx context.Context, sessionID string, prompt string, context SessionContext) (RouteDecision, error) {
	return RouteDecision{}, f.err
}
func (f *failingOrchestrator) AcceptSession(ctx context.Context, sessionID string, agent string) error {
	return f.err
}
func (f *failingOrchestrator) Dispatch(ctx context.Context, sessionID string, agent string, prompt string) (DispatchResult, error) {
	return DispatchResult{}, f.err
}

// slowOrchestrator simulates a slow dispatch.
type slowOrchestrator struct {
	delay time.Duration
}

func (s *slowOrchestrator) Route(ctx context.Context, sessionID string, prompt string, context SessionContext) (RouteDecision, error) {
	return RouteDecision{Specialist: "unit-tests", Reason: "slow test"}, nil
}
func (s *slowOrchestrator) AcceptSession(ctx context.Context, sessionID string, agent string) error {
	return nil
}
func (s *slowOrchestrator) Dispatch(ctx context.Context, sessionID string, agent string, prompt string) (DispatchResult, error) {
	select {
	case <-time.After(s.delay):
		return DispatchResult{SessionID: sessionID, Status: "completed", Agent: agent}, nil
	case <-ctx.Done():
		return DispatchResult{}, ctx.Err()
	}
}

// reuse failingAuditStore from sessions_test.go
