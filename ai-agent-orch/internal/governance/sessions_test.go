package governance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"ai-agent-orch/internal/audit"
)

func TestCreateSessionAcceptsDevTokenAndWritesAuditWithoutRawPrompt(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID: fixedIDs(
			"sess_test_generation_1",
			"evt_session_created_1",
		),
	})
	handler := NewSessionHandler(service)

	body := []byte(`{
		"agent": "unit-tests",
		"classification": "internal",
		"prompt": "write regression tests for the payment edge case"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var response CreateSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.SessionID != "sess_test_generation_1" {
		t.Fatalf("unexpected session ID %q", response.SessionID)
	}
	if response.AuditEventID != "evt_session_created_1" {
		t.Fatalf("unexpected audit event ID %q", response.AuditEventID)
	}
	if strings.Contains(rec.Body.String(), "payment edge case") {
		t.Fatalf("response must not echo raw prompt: %s", rec.Body.String())
	}

	auditBytes, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	auditText := string(auditBytes)
	if strings.Contains(auditText, "payment edge case") {
		t.Fatalf("audit file must not store raw prompt: %s", auditText)
	}
	if !strings.Contains(auditText, `"event_type":"session.created"`) {
		t.Fatalf("expected session.created audit event: %s", auditText)
	}
	expectedHash := sha256.Sum256([]byte("write regression tests for the payment edge case"))
	if !strings.Contains(auditText, hex.EncodeToString(expectedHash[:])) {
		t.Fatalf("expected prompt hash in audit event: %s", auditText)
	}
}

func TestListSessionsReturnsRecentSummariesForCurrentActor(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	if _, err := auditStore.Append(context.Background(), audit.Event{
		EventID:       "evt_usage_new",
		SessionID:     "sess_new",
		EventType:     "model.gateway_stream.completed",
		Provider:      "openrouter",
		ModelAlias:    "coding-fast",
		ModelResolved: "openrouter/x-ai/grok-build-0.1",
		TokenUsage: map[string]any{
			"prompt_tokens":     12,
			"completion_tokens": 4,
			"total_tokens":      16,
		},
		GatewayBackend:  "bifrost",
		TrustLevel:      "gateway_enforced",
		EnforcementMode: "gateway",
		RecordedAt:      time.Date(2026, 6, 8, 7, 3, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("append usage audit event: %v", err)
	}
	if _, err := auditStore.Append(context.Background(), audit.Event{
		EventID:     "evt_mcp_new",
		SessionID:   "sess_new",
		EventType:   "mcp.proxy_call",
		Reason:      "forwarded",
		MCPServerID: "playwright-cli",
		MCPToolName: "runPlaywrightTest",
		RecordedAt:  time.Date(2026, 6, 8, 7, 4, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("append mcp audit event: %v", err)
	}
	store := &recordingSessionStore{created: []SessionRecord{
		{
			SessionID:      "sess_other_actor",
			ActorSubject:   "other-dev",
			Agent:          "unit-tests",
			Classification: "internal",
			PromptSHA256:   "other-hash",
			Status:         "done",
			CreatedAt:      time.Date(2026, 6, 8, 7, 0, 0, 0, time.UTC),
		},
		{
			SessionID:      "sess_old",
			ActorSubject:   "local-dev",
			Agent:          "unit-tests",
			Classification: "internal",
			PromptSHA256:   "old-hash",
			Status:         "done",
			CreatedAt:      time.Date(2026, 6, 8, 7, 1, 0, 0, time.UTC),
		},
		{
			SessionID:      "sess_new",
			RunID:          "run_new",
			ActorSubject:   "local-dev",
			Agent:          "code-review",
			Classification: "internal",
			PromptSHA256:   "new-hash",
			Status:         "running",
			CreatedAt:      time.Date(2026, 6, 8, 7, 2, 0, 0, time.UTC),
			PermissionMode: "reviewed",
			ApprovalMode:   "manual",
			WorkspaceMode:  "local",
		},
	}}
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    auditStore,
		Sessions: store,
		ModelPricing: fakeModelPricingStore{record: ModelPricingRecord{
			Provider:               "openrouter",
			ModelID:                "x-ai/grok-build-0.1",
			PromptCostPerToken:     0.000001,
			CompletionCostPerToken: 0.000002,
		}},
	})
	handler := NewSessionHandler(service)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions?limit=1", nil)
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response ListSessionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Sessions) != 1 {
		t.Fatalf("expected one session due limit, got %#v", response.Sessions)
	}
	got := response.Sessions[0]
	if got.SessionID != "sess_new" || got.RunID != "run_new" || got.Agent != "code-review" || got.Status != "running" {
		t.Fatalf("unexpected session summary: %#v", got)
	}
	if got.UsageSummary.ModelAlias != "coding-fast" || got.UsageSummary.ModelResolved != "openrouter/x-ai/grok-build-0.1" {
		t.Fatalf("expected model attribution in session summary, got %#v", got.UsageSummary)
	}
	if got.UsageSummary.PromptTokens != 12 || got.UsageSummary.CompletionTokens != 4 || got.UsageSummary.TotalTokens != 16 {
		t.Fatalf("expected token usage in session summary, got %#v", got.UsageSummary)
	}
	if got.UsageSummary.CostSource != "pricing_table" || got.UsageSummary.EstimatedCostUSD <= 0 {
		t.Fatalf("expected pricing-table cost in session summary, got %#v", got.UsageSummary)
	}
	if got.LatestEventType != "mcp.proxy_call" || got.ToolCallCount != 1 || got.Transport != "model-gateway/bifrost" {
		t.Fatalf("expected ledger fields in session summary, got %#v", got)
	}
	if got.TrustLevel != "gateway_enforced" || got.EnforcementMode != "gateway" {
		t.Fatalf("expected trust fields in session summary, got %#v", got)
	}
	if strings.Contains(rec.Body.String(), "new-hash") || strings.Contains(rec.Body.String(), "old-hash") {
		t.Fatalf("session list must not expose prompt hashes: %s", rec.Body.String())
	}
}

func TestListSessionsRequiresDevToken(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: &recordingSessionStore{},
	})
	handler := NewSessionHandler(service)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSessionRejectsInvalidDevTokenBeforeReadingRawPrompt(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID: fixedIDs(
			"evt_session_denied_1",
		),
	})
	handler := NewSessionHandler(service)

	body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"raw secret prompt must not be read"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
	auditBytes, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	auditText := string(auditBytes)
	if strings.Contains(auditText, "raw secret prompt") {
		t.Fatalf("denied audit event must not include request body: %s", auditText)
	}
	if !strings.Contains(auditText, `"event_type":"session.denied"`) || !strings.Contains(auditText, `"reason":"invalid dev token"`) {
		t.Fatalf("expected denied audit event: %s", auditText)
	}
}

func TestCreateSessionPersistsGatewayEnforcedTrustLevel(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID: fixedIDs(
			"sess_gateway_enforced_1",
			"evt_session_created_1",
		),
	})
	handler := NewSessionHandler(service)

	body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"write tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("X-AI-Orch-Client", "ai-orch-mcp")
	req.Header.Set("X-AI-Orch-Trust-Level", "gateway_enforced")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	events, err := audit.NewFileStore(auditPath).EventsBySession(context.Background(), "sess_gateway_enforced_1")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if events[0].TrustLevel != "gateway_enforced" {
		t.Fatalf("expected gateway_enforced trust level, got %q", events[0].TrustLevel)
	}
	if events[0].EnforcementMode != "gateway" {
		t.Fatalf("expected gateway enforcement mode, got %q", events[0].EnforcementMode)
	}
}

// TestTrustedClientTokenGatesPrivilegedTrust verifies that when a trusted-client
// token is configured, a caller cannot forge a privileged trust level by merely
// claiming the ai-orch-mcp client identity: the matching shared secret is required.
func TestTrustedClientTokenGatesPrivilegedTrust(t *testing.T) {
	cases := []struct {
		name            string
		presentToken    string
		wantTrustLevel  string
		wantEnforcement string
	}{
		{name: "missing token forges nothing", presentToken: "", wantTrustLevel: "self_reported", wantEnforcement: "advisory"},
		{name: "wrong token forges nothing", presentToken: "not-the-secret", wantTrustLevel: "self_reported", wantEnforcement: "advisory"},
		{name: "correct token is honored", presentToken: "trusted-secret", wantTrustLevel: "gateway_enforced", wantEnforcement: "gateway"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
			service := NewSessionService(SessionConfig{
				DevToken:           "local-test-token",
				TrustedClientToken: "trusted-secret",
				Audit:              audit.NewFileStore(auditPath),
				NewID:              fixedIDs("sess_strict_1", "evt_strict_1"),
			})
			handler := NewSessionHandler(service)

			body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"write tests"}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer local-test-token")
			req.Header.Set("X-AI-Orch-Client", "ai-orch-mcp")
			if tc.presentToken != "" {
				req.Header.Set("X-AI-Orch-Trusted-Client-Token", tc.presentToken)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
			}
			events, err := audit.NewFileStore(auditPath).EventsBySession(context.Background(), "sess_strict_1")
			if err != nil {
				t.Fatalf("audit lookup: %v", err)
			}
			if len(events) != 1 {
				t.Fatalf("expected one event, got %d", len(events))
			}
			if events[0].TrustLevel != tc.wantTrustLevel || events[0].EnforcementMode != tc.wantEnforcement {
				t.Fatalf("trust=%q enforcement=%q, want trust=%q enforcement=%q",
					events[0].TrustLevel, events[0].EnforcementMode, tc.wantTrustLevel, tc.wantEnforcement)
			}
		})
	}
}

func TestCreateSessionAuditsResolvedContextBeforeCreatedEvent(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	store := &recordingSessionStore{}
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		Sessions: store,
		NewID: fixedIDs(
			"sess_context_1",
			"evt_context_1",
		),
		ContextResolver: staticContextResolver{context: SessionContext{
			RepoURL:      "https://example.test/team/app.git",
			Branch:       "frontend/ABC-123-login-flow",
			CommitSHA:    "0123456789abcdef0123456789abcdef01234567",
			WorkItemID:   "ABC-123",
			WorkItemType: "frontend",
			ActorHint:    "Local Developer <dev@example.test>",
			SourceSystem: "jira",
		}},
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{
		"agent": "unit-tests",
		"classification": "internal",
		"prompt": "write regression tests",
		"run_id": "run_context_1",
		"permission_mode": "full_access"
	}`)
	req.Header.Set("X-AI-Orch-Client", "ai-agent-bridge")
	req.Header.Set("X-AI-Orch-Trust-Level", "managed_client")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	events, err := audit.NewFileStore(auditPath).EventsBySession(context.Background(), "sess_context_1")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	event := events[0]
	if event.RunID != "run_context_1" {
		t.Fatalf("expected run_id in audit, got %q", event.RunID)
	}
	if event.WorkItemID != "ABC-123" || event.WorkItemType != "frontend" {
		t.Fatalf("expected resolved work item in audit, got %q/%q", event.WorkItemID, event.WorkItemType)
	}
	if event.CommitSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("expected resolved commit in audit, got %q", event.CommitSHA)
	}
	if event.ActorHint != "Local Developer <dev@example.test>" || event.SourceSystem != "jira" {
		t.Fatalf("expected resolved actor/source in audit, got %q/%q", event.ActorHint, event.SourceSystem)
	}
	if event.PermissionMode != "full_access" || event.ApprovalMode != "yolo" {
		t.Fatalf("expected full_access/yolo audit modes, got %q/%q", event.PermissionMode, event.ApprovalMode)
	}
	if event.TrustLevel != "managed_client" {
		t.Fatalf("expected managed_client trust level, got %q", event.TrustLevel)
	}
	if event.EnforcementMode != "managed" {
		t.Fatalf("expected managed enforcement mode, got %q", event.EnforcementMode)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected one stored session, got %d", len(store.created))
	}
	rec0 := store.created[0]
	if rec0.RunID != "run_context_1" || rec0.PermissionMode != "full_access" || rec0.ApprovalMode != "yolo" {
		t.Fatalf("expected stored run and modes, got %#v", rec0)
	}
	if rec0.RepoURL != "https://example.test/team/app.git" || rec0.Branch != "frontend/ABC-123-login-flow" {
		t.Fatalf("expected stored resolved repo/branch, got %#v", rec0)
	}
}

func TestCreateSessionRequiresWorkItemWhenEnabled(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken:        "local-test-token",
		Audit:           audit.NewFileStore(auditPath),
		RequireWorkItem: true,
		NewID: fixedIDs(
			"evt_denied_work_item",
		),
		ContextResolver: staticContextResolver{context: SessionContext{
			Branch:    "main",
			CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		}},
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"write tests"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected 428, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "work item ID is required") {
		t.Fatalf("expected work item guidance, got %s", rec.Body.String())
	}
}

func TestCreateSessionAllowsFeatureBranchWithWorkItemWhenRequired(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken:        "local-test-token",
		Audit:           audit.NewFileStore(auditPath),
		RequireWorkItem: true,
		NewID: fixedIDs(
			"sess_work_item_required",
			"evt_work_item_required",
		),
		ContextResolver: staticContextResolver{context: SessionContext{
			Branch:       "feature/WORK-123-governance-fixes",
			WorkItemID:   "WORK-123",
			WorkItemType: "feature",
			CommitSHA:    "0123456789abcdef0123456789abcdef01234567",
		}},
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"write tests"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	events, err := audit.NewFileStore(auditPath).EventsBySession(context.Background(), "sess_work_item_required")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].WorkItemID != "WORK-123" || events[0].WorkItemType != "feature" {
		t.Fatalf("expected resolved work item audit event, got %#v", events)
	}
}

func TestCreateSessionDoesNotTrustBareTrustHeader(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID: fixedIDs(
			"sess_spoofed_trust_1",
			"evt_session_created_1",
		),
	})
	handler := NewSessionHandler(service)

	body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"write tests"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("X-AI-Orch-Trust-Level", "managed_client")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	events, err := audit.NewFileStore(auditPath).EventsBySession(context.Background(), "sess_spoofed_trust_1")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if events[0].TrustLevel != "self_reported" || events[0].EnforcementMode != "advisory" {
		t.Fatalf("bare trust header must not upgrade audit label, got %#v", events[0])
	}
}

func TestCreateSessionDefaultsReviewedManualModes(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	store := &recordingSessionStore{}
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		Sessions: store,
		NewID: fixedIDs(
			"sess_modes_default",
			"evt_modes_default",
		),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"write tests"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	events, err := audit.NewFileStore(auditPath).EventsBySession(context.Background(), "sess_modes_default")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if events[0].PermissionMode != "reviewed" || events[0].ApprovalMode != "manual" {
		t.Fatalf("expected reviewed/manual default, got %q/%q", events[0].PermissionMode, events[0].ApprovalMode)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected one stored session, got %d", len(store.created))
	}
	if store.created[0].PermissionMode != "reviewed" || store.created[0].ApprovalMode != "manual" {
		t.Fatalf("expected stored reviewed/manual default, got %#v", store.created[0])
	}
}

func TestCreateSessionRejectsInvalidPermissionAndApprovalModes(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{
		"agent": "unit-tests",
		"classification": "internal",
		"prompt": "write tests",
		"permission_mode": "root",
		"approval_mode": "whatever"
	}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "permission_mode") {
		t.Fatalf("expected permission_mode validation message, got %s", rec.Body.String())
	}
}

func TestCreateSessionFailsClosedWhenDevTokenEmpty(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		Audit: audit.NewFileStore(auditPath),
		NewID: fixedIDs(
			"evt_missing_dev_token",
		),
	})
	handler := NewSessionHandler(service)

	body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"hello world"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer any-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, "hello world") {
		t.Fatalf("dev-token denial audit event must not include request body: %s", auditText)
	}
	if !strings.Contains(auditText, `"reason":"dev token not configured"`) {
		t.Fatalf("expected dev-token denial audit event: %s", auditText)
	}
}

func TestCreateSessionKillSwitchBlocksBeforeReadingRawPrompt(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken:   "local-test-token",
		Audit:      audit.NewFileStore(auditPath),
		KillSwitch: true,
		NewID: fixedIDs(
			"evt_kill_switch_1",
		),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"kill switch raw prompt must not be read"}`)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected 423, got %d: %s", rec.Code, rec.Body.String())
	}
	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, "kill switch raw prompt") {
		t.Fatalf("kill-switch audit event must not include request body: %s", auditText)
	}
	if !strings.Contains(auditText, `"reason":"kill switch enabled"`) {
		t.Fatalf("expected kill switch audit event: %s", auditText)
	}
}

func TestCreateSessionRejectsClassificationAboveConfiguredMaximum(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken:          "local-test-token",
		Audit:             audit.NewFileStore(auditPath),
		ClassificationMax: "internal",
		NewID: fixedIDs(
			"evt_classification_denied_1",
		),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"restricted","prompt":"restricted repo details must not dispatch"}`)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, "restricted repo details") {
		t.Fatalf("classification-denied audit event must not include raw prompt: %s", auditText)
	}
	if !strings.Contains(auditText, `"reason":"classification restricted exceeds max internal"`) {
		t.Fatalf("expected classification denial audit event: %s", auditText)
	}
}

func TestCreateSessionRejectsPromptWithSecretBeforeDispatch(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID: fixedIDs(
			"evt_secret_denied_1",
		),
	})
	handler := NewSessionHandler(service)

	fakeToken := "sk-or-v1-" + "test1234567890"
	req := authorizedSessionRequest(fmt.Sprintf(`{"agent":"unit-tests","classification":"internal","prompt":"use OPENROUTER_API_KEY=%s for this run"}`, fakeToken))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, fakeToken) || strings.Contains(auditText, "OPENROUTER_API_KEY") {
		t.Fatalf("secret-denied audit event must not include raw secret: %s", auditText)
	}
	if !strings.Contains(auditText, `"reason":"secret detected"`) || !strings.Contains(auditText, `"findings":["openrouter_api_key"]`) {
		t.Fatalf("expected secret finding in audit event: %s", auditText)
	}
}

func TestCreateSessionRejectsEstimatedCostAboveCap(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken:          "local-test-token",
		Audit:             audit.NewFileStore(auditPath),
		CostCapEnabled:    true,
		SessionCostCapUSD: 0.25,
		NewID: fixedIDs(
			"evt_cost_denied_1",
		),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"ordinary prompt","estimated_cost_usd":0.30}`)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d: %s", rec.Code, rec.Body.String())
	}
	auditText := readAuditText(t, auditPath)
	if !strings.Contains(auditText, `"reason":"cost cap exceeded"`) {
		t.Fatalf("expected cost denial audit event: %s", auditText)
	}
	if !strings.Contains(auditText, `"estimated_cost_usd":0.3`) || !strings.Contains(auditText, `"cost_cap_usd":0.25`) {
		t.Fatalf("expected cost metadata in audit event: %s", auditText)
	}
}

func TestCreateSessionRecordsEstimatedCostWithoutEnforcingCapByDefault(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(auditPath),
		NewID: fixedIDs(
			"sess_cost_recorded_1",
			"evt_cost_recorded_1",
		),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"ordinary prompt","estimated_cost_usd":1.25}`)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 when cost cap disabled, got %d: %s", rec.Code, rec.Body.String())
	}
	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, "cost cap exceeded") {
		t.Fatalf("cost cap should not be enforced by default: %s", auditText)
	}
	if !strings.Contains(auditText, `"estimated_cost_usd":1.25`) {
		t.Fatalf("expected estimated cost to be recorded: %s", auditText)
	}
	if strings.Contains(auditText, `"cost_cap_usd"`) {
		t.Fatalf("disabled cost cap should not be recorded as an active cap: %s", auditText)
	}
}

func TestCreateSessionLocalIdentityHeaderOverridesDevTokenActorOnly(t *testing.T) {
	store := &recordingSessionStore{}
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions: store,
		NewID: fixedIDs(
			"sess_requested_by",
			"evt_requested_by",
		),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"ordinary prompt"}`)
	req.Header.Set("X-AI-Orch-Local-Identity", "developer:local")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.created) != 1 {
		t.Fatalf("expected session to persist once, got %d", len(store.created))
	}
	if store.created[0].ActorSubject != "developer:local" {
		t.Fatalf("expected local identity header actor for dev token, got %q", store.created[0].ActorSubject)
	}
}

func TestCreateSessionRequestedByCannotOverrideOIDCSubject(t *testing.T) {
	store := &recordingSessionStore{}
	service := NewSessionService(SessionConfig{
		Authorizer: fixedAuthorizer{subject: "oidc-user"},
		Audit:      audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:   store,
		NewID: fixedIDs(
			"sess_oidc_subject",
			"evt_oidc_subject",
		),
	})
	handler := NewSessionHandler(service)

	body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"ordinary prompt","requested_by":"spoofed-user"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer oidc-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.created) != 1 {
		t.Fatalf("expected session to persist once, got %d", len(store.created))
	}
	if store.created[0].ActorSubject != "oidc-user" {
		t.Fatalf("expected OIDC subject to win, got %q", store.created[0].ActorSubject)
	}
}

func TestCreateSessionRejectsInvalidLocalIdentityHeader(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"ordinary prompt"}`)
	req.Header.Set("X-AI-Orch-Local-Identity", "bad actor")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSessionRejectsInvalidRequestedBy(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest("{\"agent\":\"unit-tests\",\"classification\":\"internal\",\"prompt\":\"ordinary prompt\",\"requested_by\":\"bad actor\"}")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateSessionFailsClosedWhenAuditWriteFails(t *testing.T) {
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    failingAuditStore{},
		NewID: fixedIDs(
			"sess_should_not_escape",
			"evt_should_fail",
		),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"normal prompt"}`)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sess_should_not_escape") {
		t.Fatalf("session ID must not be returned when audit write fails: %s", rec.Body.String())
	}
}

func TestCreateSessionDoesNotPersistWhenAuditWriteFails(t *testing.T) {
	store := &recordingSessionStore{}
	service := NewSessionService(SessionConfig{
		DevToken: "local-test-token",
		Audit:    failingAuditStore{},
		Sessions: store,
		NewID: fixedIDs(
			"sess_should_not_persist",
			"evt_should_fail",
		),
	})
	handler := NewSessionHandler(service)

	req := authorizedSessionRequest(`{"agent":"unit-tests","classification":"internal","prompt":"normal prompt"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.created) != 0 {
		t.Fatalf("session store must not persist when audit fails: %#v", store.created)
	}
}

func fixedIDs(ids ...string) func(string) string {
	index := 0
	return func(prefix string) string {
		if index >= len(ids) {
			return prefix + "_extra"
		}
		id := ids[index]
		index++
		return id
	}
}

func authorizedSessionRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestCreateAutoGatewaySessionAppliesGovernanceAndTokenBinding(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	store := &recordingSessionStore{}
	service := NewSessionService(SessionConfig{
		Audit:              auditStore,
		Sessions:           store,
		ClassificationMax:  "internal",
		TrustedClientToken: "trusted-client",
		NewID: fixedIDs(
			"sess_auto_1",
			"evt_auto_created_1",
			"sgt_auto_secret_1",
		),
	})

	result, err := service.CreateAutoGatewaySession(context.Background(), AutoGatewaySessionRequest{
		ActorSubject:       "dev@example.test",
		Classification:     "internal",
		PromptSHA256:       "sha256:req",
		ModelAlias:         "coding-fast",
		Client:             "ai-orch-mcp",
		Endpoint:           "chat.completions",
		RawRequestBody:     []byte(`{"model":"coding-fast","messages":[{"role":"user","content":"hello"}]}`),
		TrustedClientToken: "trusted-client",
		WorkItemID:         "WORK-123",
		WorkItemType:       "test",
		Branch:             "test/WORK-123-auto-session",
		Intent:             "Need direct model exploration before choosing a specialist",
	})
	if err != nil {
		t.Fatalf("create auto session: %v", err)
	}
	if result.Record.SessionID != "sess_auto_1" || result.GatewayToken != "sgt_auto_secret_1" {
		t.Fatalf("unexpected auto session result: %#v", result)
	}
	if len(store.created) != 1 || store.created[0].GatewayTokenSHA256 == "" {
		t.Fatalf("expected stored gateway token hash, got %#v", store.created)
	}
	goodHash := sha256.Sum256([]byte("sgt_auto_secret_1"))
	if store.created[0].GatewayTokenSHA256 != hex.EncodeToString(goodHash[:]) {
		t.Fatalf("unexpected gateway token hash: %s", store.created[0].GatewayTokenSHA256)
	}
	if store.created[0].Intent != "Need direct model exploration before choosing a specialist" {
		t.Fatalf("expected auto-session intent reason, got %q", store.created[0].Intent)
	}
	events, err := auditStore.EventsBySession(context.Background(), "sess_auto_1")
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].TrustLevel != "gateway_enforced" || events[0].EnforcementMode != "gateway" {
		t.Fatalf("expected trusted auto-session audit, got %#v", events)
	}
}

func TestCreateAutoGatewaySessionDefaultsToSelfReportedTrust(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	service := NewSessionService(SessionConfig{
		Audit:              auditStore,
		Sessions:           &recordingSessionStore{},
		ClassificationMax:  "internal",
		TrustedClientToken: "trusted-client",
	})
	result, err := service.CreateAutoGatewaySession(context.Background(), AutoGatewaySessionRequest{
		ActorSubject:   "dev@example.test",
		Classification: "internal",
		PromptSHA256:   "sha256:req",
		ModelAlias:     "coding-fast",
		Client:         "ai-orch-mcp",
		Endpoint:       "chat.completions",
		RawRequestBody: []byte(`{"model":"coding-fast","messages":[{"role":"user","content":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("create auto session: %v", err)
	}
	events, err := auditStore.EventsBySession(context.Background(), result.Record.SessionID)
	if err != nil {
		t.Fatalf("audit lookup: %v", err)
	}
	if len(events) != 1 || events[0].TrustLevel != "self_reported" || events[0].EnforcementMode != "advisory" {
		t.Fatalf("expected self-reported trust, got %#v", events)
	}
}

func TestCreateAutoGatewaySessionDeniesGovernanceFailures(t *testing.T) {
	secretBody := []byte(`{"model":"coding-fast","messages":[{"role":"user","content":"OPENROUTER_API_KEY=sk-or-v1-secretsecretsecret"}]}`)
	cases := []struct {
		name     string
		config   SessionConfig
		request  AutoGatewaySessionRequest
		wantPart string
	}{
		{
			name:     "global kill switch",
			config:   SessionConfig{KillSwitch: true},
			request:  validAutoGatewaySessionRequest(),
			wantPart: "kill switch",
		},
		{
			name:     "work item required",
			config:   SessionConfig{RequireWorkItem: true},
			request:  validAutoGatewaySessionRequest(),
			wantPart: "work item",
		},
		{
			name:     "secret detected",
			config:   SessionConfig{},
			request:  withAutoBody(validAutoGatewaySessionRequest(), secretBody),
			wantPart: "secret detected",
		},
		{
			name:     "classification ceiling",
			config:   SessionConfig{ClassificationMax: "internal"},
			request:  withAutoClassification(validAutoGatewaySessionRequest(), "confidential"),
			wantPart: "classification confidential exceeds max internal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
			cfg := tc.config
			cfg.Audit = auditStore
			cfg.Sessions = &recordingSessionStore{}
			if cfg.ClassificationMax == "" {
				cfg.ClassificationMax = "internal"
			}
			service := NewSessionService(cfg)
			_, err := service.CreateAutoGatewaySession(context.Background(), tc.request)
			if err == nil || !strings.Contains(err.Error(), tc.wantPart) {
				t.Fatalf("expected error containing %q, got %v", tc.wantPart, err)
			}
		})
	}
}

func validAutoGatewaySessionRequest() AutoGatewaySessionRequest {
	return AutoGatewaySessionRequest{
		ActorSubject:   "dev@example.test",
		Classification: "internal",
		PromptSHA256:   "sha256:req",
		ModelAlias:     "coding-fast",
		Client:         "opencode",
		Endpoint:       "chat.completions",
		RawRequestBody: []byte(`{"model":"coding-fast","messages":[{"role":"user","content":"hello"}]}`),
	}
}

func withAutoBody(req AutoGatewaySessionRequest, body []byte) AutoGatewaySessionRequest {
	req.RawRequestBody = body
	return req
}

func withAutoClassification(req AutoGatewaySessionRequest, classification string) AutoGatewaySessionRequest {
	req.Classification = classification
	return req
}

func readAuditText(t *testing.T, path string) string {
	t.Helper()
	auditBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	return string(auditBytes)
}

type failingAuditStore struct{}

func (failingAuditStore) Append(context.Context, audit.Event) (audit.Event, error) {
	return audit.Event{}, errors.New("audit unavailable")
}

func (failingAuditStore) EventsBySession(context.Context, string) ([]audit.Event, error) {
	return nil, errors.New("audit unavailable")
}

type recordingSessionStore struct {
	created []SessionRecord
}

func (s *recordingSessionStore) Create(_ context.Context, rec SessionRecord) error {
	s.created = append(s.created, rec)
	return nil
}

func (s *recordingSessionStore) Get(_ context.Context, sessionID string) (SessionRecord, error) {
	for _, rec := range s.created {
		if rec.SessionID == sessionID {
			return rec, nil
		}
	}
	return SessionRecord{}, errors.New("session not found")
}

func (s *recordingSessionStore) ListRecent(_ context.Context, actorSubject string, limit int) ([]SessionRecord, error) {
	var records []SessionRecord
	for _, rec := range s.created {
		if rec.ActorSubject == actorSubject {
			records = append(records, rec)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func (s *recordingSessionStore) UpdateStatus(_ context.Context, sessionID string, status string) error {
	for i := range s.created {
		if s.created[i].SessionID == sessionID {
			s.created[i].Status = status
			return nil
		}
	}
	return nil
}

func (s *recordingSessionStore) SetRoutedAgent(_ context.Context, sessionID string, agent string) error {
	for i := range s.created {
		if s.created[i].SessionID == sessionID {
			s.created[i].RoutedAgent = agent
			return nil
		}
	}
	return nil
}

func (s *recordingSessionStore) CompareAndSwapStatus(context.Context, string, string, string) error {
	return nil
}

type fixedAuthorizer struct {
	subject string
}

func (f fixedAuthorizer) Validate(context.Context, string) (string, bool) {
	return f.subject, f.subject != ""
}

type staticContextResolver struct {
	context SessionContext
}

func (r staticContextResolver) Resolve() SessionContext {
	return r.context
}
