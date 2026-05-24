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
		if a.Name == "test-generation" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected test-generation in agent list")
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
	orch := &fakeOrchestrator{specialist: "test-generation", reason: "testing keyword match"}
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
	if result["specialist"] != "test-generation" {
		t.Fatalf("expected test-generation, got %v", result["specialist"])
	}

	auditText := readAuditText(t, auditPath)
	if !strings.Contains(auditText, `"event_type":"router.specialist.selected"`) {
		t.Fatalf("expected router audit event: %s", auditText)
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
	handler := NewConfirmHandler(service, orch)

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
	handler := NewConfirmHandler(service, orch)

	body := []byte(`{"agent":"test-generation"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess_123/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d: %s", rec.Code, rec.Body.String())
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
	handler := NewConfirmHandler(service, orch)

	body := []byte(`{"agent":"test-generation"}`)
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

func TestConfirmHandlerWithEventsRequiresCachedPrompt(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	orch := &fakeOrchestrator{}
	handler := NewConfirmHandlerWithEvents(service, orch, NewEventStore())

	body := []byte(`{"agent":"test-generation"}`)
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

	ctx, cancel := context.WithCancel(context.Background())
	body := []byte(`{"agent":"test-generation"}`)
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
	store.Block("agent", "test-generation")
	service := NewSessionService(SessionConfig{DevToken: "local-test-token"})
	handler := NewAdminHandler(store, service)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/killswitch", nil)
	req.Header.Set("Authorization", "Bearer local-test-token")
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

func TestAdminKillSwitchFailsClosedWhenDevTokenMissing(t *testing.T) {
	store := NewMemoryKillSwitch()
	service := NewSessionService(SessionConfig{})
	handler := NewAdminHandler(store, service)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/killswitch", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminKillSwitchBlockAndUnblock(t *testing.T) {
	store := NewMemoryKillSwitch()
	service := NewSessionService(SessionConfig{DevToken: "local-test-token"})
	handler := NewAdminHandler(store, service)

	// Block
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/killswitch/agent/test-generation", nil)
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on block, got %d: %s", rec.Code, rec.Body.String())
	}
	if !store.IsBlocked("agent", "test-generation") {
		t.Fatalf("expected agent to be blocked")
	}

	// Unblock
	req = httptest.NewRequest(http.MethodDelete, "/v1/admin/killswitch/agent/test-generation", nil)
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on unblock, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.IsBlocked("agent", "test-generation") {
		t.Fatalf("expected agent to be unblocked")
	}
}

func TestAgentKillSwitchBlocksSessionCreation(t *testing.T) {
	store := NewMemoryKillSwitch()
	if err := store.Block("agent", "test-generation"); err != nil {
		t.Fatalf("block agent: %v", err)
	}
	service := NewSessionService(SessionConfig{
		DevToken:        "local-test-token",
		Audit:           audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		KillSwitchStore: store,
	})
	handler := NewSessionHandler(service)

	body := []byte(`{"agent":"test-generation","classification":"internal","prompt":"write tests"}`)
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
	if err := store.Block("agent", "test-generation"); err != nil {
		t.Fatalf("block agent: %v", err)
	}
	service := NewSessionService(SessionConfig{
		DevToken:        "local-test-token",
		Audit:           audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		KillSwitchStore: store,
	})
	orch := &fakeOrchestrator{}
	handler := NewConfirmHandler(service, orch)

	body := []byte(`{"agent":"test-generation"}`)
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

	body := []byte(`{"agent":"test-generation","classification":"internal","prompt":"write tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	body = []byte(`{"agent":"test-generation","classification":"restricted","prompt":"write tests"}`)
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
	specialist     string
	reason         string
	err            error
	acceptErr      error
	dispatchErr    error
	dispatchPrompt string
}

func (f *fakeOrchestrator) Route(ctx context.Context, sessionID string, prompt string) (RouteDecision, error) {
	if f.err != nil {
		return RouteDecision{}, f.err
	}
	return RouteDecision{Specialist: f.specialist, Reason: f.reason}, nil
}

func (f *fakeOrchestrator) AcceptSession(ctx context.Context, sessionID string, agent string) error {
	if f.acceptErr != nil {
		return f.acceptErr
	}
	return nil
}

func (f *fakeOrchestrator) Dispatch(ctx context.Context, sessionID string, agent string, prompt string) (DispatchResult, error) {
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
			{Type: "patch", Payload: `{"patchId":"patch_1","files":[]}`},
			{Type: "done", Payload: "mock execution complete"},
		},
	}, nil
}

type contextProbeOrchestrator struct {
	dispatched chan error
}

func (o *contextProbeOrchestrator) Route(ctx context.Context, sessionID string, prompt string) (RouteDecision, error) {
	return RouteDecision{Specialist: "test-generation", Reason: "test"}, nil
}

func (o *contextProbeOrchestrator) AcceptSession(ctx context.Context, sessionID string, agent string) error {
	return nil
}

func (o *contextProbeOrchestrator) Dispatch(ctx context.Context, sessionID string, agent string, prompt string) (DispatchResult, error) {
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
				{Type: "patch", Payload: `{"patchId":"patch_ctx","files":[]}`},
				{Type: "done", Payload: "ok"},
			},
		}, nil
	}
}
