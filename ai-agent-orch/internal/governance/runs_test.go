package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestRunHandlerCreatesSessionAndRoutesWithContext(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	store := &recordingSessionStore{}
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		Sessions: store,
		NewID: fixedIDs(
			"run_generated_1",
			"sess_run_1",
			"evt_session_run_1",
		),
	})
	orch := &routeCaptureOrchestrator{
		specialist: "unit-tests",
		reason:     "testing keyword match",
	}
	handler := NewRunHandler(service, orch)

	body := []byte(`{
		"agent": "unit-tests",
		"classification": "internal",
		"prompt": "write unit tests for the parser",
		"repo_url": "https://example.test/team/app.git",
		"branch": "test/ABC-123-parser",
		"commit_sha": "0123456789abcdef0123456789abcdef01234567",
		"work_item_id": "ABC-123",
		"work_item_type": "test",
		"actor_hint": "developer@example.test",
		"source_system": "jira",
		"permission_mode": "reviewed",
		"approval_mode": "manual"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AI-Orch-Client", "ai-agent-bridge")
	req.Header.Set("X-AI-Orch-Trust-Level", "managed_client")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var response RunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode run response: %v", err)
	}
	if response.RunID != "run_generated_1" || response.SessionID != "sess_run_1" {
		t.Fatalf("unexpected run/session response: %#v", response)
	}
	if response.Status != "awaiting_confirmation" || response.NextGate != "confirm" {
		t.Fatalf("expected awaiting_confirmation/confirm, got %#v", response)
	}
	if response.Specialist != "unit-tests" {
		t.Fatalf("expected unit-tests specialist, got %q", response.Specialist)
	}
	if response.SSEURL != "/v1/sessions/sess_run_1/events" {
		t.Fatalf("unexpected SSE URL %q", response.SSEURL)
	}

	if orch.sessionID != "sess_run_1" || orch.prompt != "write unit tests for the parser" {
		t.Fatalf("unexpected route call: %#v", orch)
	}
	if orch.context.RepoURL != "https://example.test/team/app.git" || orch.context.Branch != "test/ABC-123-parser" {
		t.Fatalf("expected routed project context, got %#v", orch.context)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected one stored session, got %d", len(store.created))
	}
	if store.created[0].RunID != "run_generated_1" || store.created[0].Status != "awaiting_confirmation" {
		t.Fatalf("expected stored run awaiting confirmation, got %#v", store.created[0])
	}

	events, err := audit.NewFileStore(auditPath).EventsBySession(context.Background(), "sess_run_1")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected session and route events, got %d", len(events))
	}
	if events[0].RunID != "run_generated_1" || events[0].PermissionMode != "reviewed" || events[0].ApprovalMode != "manual" {
		t.Fatalf("expected session.created run and modes, got %#v", events[0])
	}
	if events[1].RunID != "run_generated_1" || events[1].EventType != "router.specialist.selected" {
		t.Fatalf("expected linked route event with run id, got %#v", events[1])
	}
	if events[0].TrustLevel != "managed_client" || events[0].EnforcementMode != "managed" ||
		events[1].TrustLevel != "managed_client" || events[1].EnforcementMode != "managed" {
		t.Fatalf("expected managed trust metadata on run events, got %#v / %#v", events[0], events[1])
	}
}

type routeCaptureOrchestrator struct {
	specialist string
	reason     string
	sessionID  string
	prompt     string
	context    SessionContext
}

func (r *routeCaptureOrchestrator) Route(ctx context.Context, sessionID string, prompt string, context SessionContext) (RouteDecision, error) {
	r.sessionID = sessionID
	r.prompt = prompt
	r.context = context
	return RouteDecision{Specialist: r.specialist, Reason: r.reason}, nil
}

func (r *routeCaptureOrchestrator) AcceptSession(context.Context, string, string) error {
	return nil
}

func (r *routeCaptureOrchestrator) Dispatch(context.Context, string, string, string, string) (DispatchResult, error) {
	return DispatchResult{}, nil
}
