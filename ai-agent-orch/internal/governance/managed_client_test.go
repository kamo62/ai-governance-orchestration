package governance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestManagedClientEvidenceRejectsInvalidExpiredAndSharedCredentials(t *testing.T) {
	now := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	credStore, err := NewSQLiteDeveloperCredentialStore(":memory:")
	if err != nil {
		t.Fatalf("credential store: %v", err)
	}
	defer credStore.Close()
	_, token, err := credStore.Issue(context.Background(), DeveloperCredentialIssue{
		ActorSubject: "dev@example.test",
		Client:       "t3code",
		Now:          now.Add(-91 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}

	handler := NewManagedClientEvidenceHandler(ManagedClientEvidenceConfig{
		Credentials: credStore,
		Audit:       audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sessions:    &recordingSessionStore{},
		Now:         func() time.Time { return now },
	})

	body := managedClientBatchJSON("evt_1", "session_start")
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"bad token", "air_bad"},
		{"expired runtime credential", token},
		{"shared dev token is not accepted", "dev-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/managed-client/evidence", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestManagedClientEvidenceValidatesSchema(t *testing.T) {
	handler, token, _, _ := newManagedClientEvidenceTestRig(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/managed-client/evidence", strings.NewReader(`{"events":[{"event_id":"evt_bad","schema_version":"v1","client":"t3code","client_session_id":"client_1","event_type":"prompt","timestamp":"2026-07-02T10:00:00Z"}]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "schema_version") {
		t.Fatalf("expected schema validation error, got %s", rec.Body.String())
	}
}

func TestManagedClientEvidenceRejectsMixedClientSessions(t *testing.T) {
	handler, token, _, _ := newManagedClientEvidenceTestRig(t)
	body := []byte(`{"events":[
		{"event_id":"evt_1","schema_version":"v0","client":"t3code","client_session_id":"client_1","event_type":"prompt","timestamp":"2026-07-02T10:00:00Z"},
		{"event_id":"evt_2","schema_version":"v0","client":"other","client_session_id":"client_2","event_type":"prompt","timestamp":"2026-07-02T10:00:01Z"}
	]}`)
	rec := postManagedClientBatch(handler, token, body)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "events[1]") {
		t.Fatalf("expected 400 naming events[1], got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestManagedClientEvidenceIngestsBatchIdempotentlyWithProvenanceAndSession(t *testing.T) {
	handler, token, auditStore, sessions := newManagedClientEvidenceTestRig(t)
	body := []byte(`{"events":[
		{"event_id":"evt_start","schema_version":"v0","client":"t3code","client_session_id":"client_1","event_type":"session_start","repo":{"remote":"https://user:secret@example.test/acme/repo.git","branch":"feature/ABC-123-managed","commit":"abc123"},"timestamp":"2026-07-02T10:00:00Z"},
		{"event_id":"evt_prompt","schema_version":"v0","client":"t3code","client_session_id":"client_1","event_type":"prompt","timestamp":"2026-07-02T10:01:00Z","content":"please fix the billing bug"},
		{"event_id":"evt_permission","schema_version":"v0","client":"t3code","client_session_id":"client_1","event_type":"permission_decision","timestamp":"2026-07-02T10:01:30Z","permission_decision":{"tool":"Bash","decision":"denied","decider":"auto_policy","reason":"blocked command"}},
		{"event_id":"evt_tool","schema_version":"v0","client":"t3code","client_session_id":"client_1","event_type":"tool_execution","timestamp":"2026-07-02T10:02:00Z","tool":{"name":"Read","started_at":"2026-07-02T10:01:30Z","ended_at":"2026-07-02T10:01:31Z","status":"ok"}},
		{"event_id":"evt_file","schema_version":"v0","client":"t3code","client_session_id":"client_1","event_type":"file_change","timestamp":"2026-07-02T10:03:00Z","file_change":{"paths":["internal/billing.go"],"diff":"diff --git a/internal/billing.go b/internal/billing.go\n+fix"}},
		{"event_id":"evt_usage","schema_version":"v0","client":"t3code","client_session_id":"client_1","event_type":"token_usage","timestamp":"2026-07-02T10:04:00Z","token_usage":{"model":"gpt-5.5","input_tokens":30,"output_tokens":12,"source":"client_reported"}},
		{"event_id":"evt_usage","schema_version":"v0","client":"t3code","client_session_id":"client_1","event_type":"token_usage","timestamp":"2026-07-02T10:04:00Z","token_usage":{"model":"gpt-5.5","input_tokens":30,"output_tokens":12,"source":"client_reported"}}
	]}`)

	rec := postManagedClientBatch(handler, token, body)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp ManagedClientEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Accepted != 6 || resp.Duplicate != 1 || resp.SessionID == "" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if len(sessions.created) != 1 {
		t.Fatalf("expected one managed session, got %#v", sessions.created)
	}
	if sessions.created[0].ActorSubject != "dev@example.test" || sessions.created[0].ClientSessionID != "client_1" || sessions.created[0].SourceSystem != "t3code" {
		t.Fatalf("unexpected session record: %#v", sessions.created[0])
	}
	if strings.Contains(sessions.created[0].RepoURL, "secret") {
		t.Fatalf("repo URL leaked credentials: %q", sessions.created[0].RepoURL)
	}

	events, err := auditStore.EventsBySession(context.Background(), resp.SessionID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 7 {
		t.Fatalf("expected session event plus six client events, got %d: %#v", len(events), events)
	}
	for _, ev := range events {
		if ev.Actor != "dev@example.test" || ev.TrustLevel != "managed_client" || ev.EnforcementMode != "advisory" || ev.CorrelationSubject != "managed_client" {
			t.Fatalf("expected managed-client provenance on %#v", ev)
		}
	}
	if events[2].PromptSHA256 == "" || events[2].RawPromptStored {
		t.Fatalf("prompt event should store only a hash, got %#v", events[2])
	}
	if events[1].EventID == "evt_start" || events[1].ClientEventID != "evt_start" {
		t.Fatalf("managed client event must use a server audit ID and preserve the client ID: %#v", events[1])
	}
	if events[3].EventType != "managed_client.permission_decision" || events[3].MCPToolName != "Bash" || events[3].PatchDecision != "denied" || events[3].AuthMode != "auto_policy" || events[3].Reason != "blocked command" {
		t.Fatalf("permission decision should store command, decision, decider and reason, got %#v", events[3])
	}
	if events[5].PatchID == "" || events[5].RequestSHA256 == "" {
		t.Fatalf("file change should store diff hash evidence, got %#v", events[5])
	}
	if events[6].EventType != "managed_client.token_usage" || events[6].TokenUsage["source"] != "client_reported" {
		t.Fatalf("expected client-reported usage event, got %#v", events[6])
	}

	rec = postManagedClientBatch(handler, token, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected second batch 202, got %d: %s", rec.Code, rec.Body.String())
	}
	events, err = auditStore.EventsBySession(context.Background(), resp.SessionID)
	if err != nil {
		t.Fatalf("events after duplicate batch: %v", err)
	}
	if len(events) != 7 {
		t.Fatalf("duplicate replay appended events: %d", len(events))
	}
}

func TestManagedClientEvidenceValidatesPermissionDecision(t *testing.T) {
	handler, token, _, _ := newManagedClientEvidenceTestRig(t)
	body := []byte(`{"events":[{"event_id":"evt_permission_bad","schema_version":"v0","client":"t3code","client_session_id":"client_1","event_type":"permission_decision","timestamp":"2026-07-02T10:00:00Z","permission_decision":{"tool":"Bash","decision":"maybe","decider":"auto_policy"}}]}`)

	rec := postManagedClientBatch(handler, token, body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "permission_decision.decision") {
		t.Fatalf("expected permission decision validation error, got %s", rec.Body.String())
	}
}

func TestManagedClientEvidenceRejectsOversizedBatch(t *testing.T) {
	handler, token, _, _ := newManagedClientEvidenceTestRig(t)
	var batch struct {
		Events []ManagedClientEvidenceEvent `json:"events"`
	}
	for i := 0; i < managedClientMaxBatchEvents+1; i++ {
		batch.Events = append(batch.Events, ManagedClientEvidenceEvent{
			EventID:         "evt_many_" + string(rune('a'+i%26)),
			SchemaVersion:   "v0",
			Client:          "t3code",
			ClientSessionID: "client_many",
			EventType:       "session_start",
			Timestamp:       time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC),
		})
	}
	body, _ := json.Marshal(batch)

	rec := postManagedClientBatch(handler, token, body)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestManagedClientEvidenceReceiptReservationsDeduplicateConcurrentReplay(t *testing.T) {
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	handler, token, receipts, sessions := newManagedClientEvidenceReceiptTestRig(t, auditStore)
	defer receipts.Close()
	body := managedClientBatchJSON("evt_concurrent", "prompt")

	start := make(chan struct{})
	responses := make(chan ManagedClientEvidenceResponse, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := postManagedClientBatch(handler, token, body)
			if rec.Code != http.StatusAccepted {
				errs <- fmt.Errorf("expected 202, got %d: %s", rec.Code, rec.Body.String())
				return
			}
			var response ManagedClientEvidenceResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				errs <- err
				return
			}
			responses <- response
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(responses)
	for err := range errs {
		t.Fatal(err)
	}

	accepted, duplicates := 0, 0
	var sessionID string
	for response := range responses {
		accepted += response.Accepted
		duplicates += response.Duplicate
		sessionID = response.SessionID
	}
	if accepted != 1 || duplicates != 1 || sessionID == "" {
		t.Fatalf("unexpected concurrent responses: accepted=%d duplicates=%d session=%q", accepted, duplicates, sessionID)
	}
	if len(sessions.created) != 1 {
		t.Fatalf("concurrent replay created multiple sessions: %#v", sessions.created)
	}
	events, err := auditStore.EventsBySession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("read audit events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("concurrent replay appended duplicate ledger events: %#v", events)
	}
}

func TestManagedClientEvidenceReleasesReceiptWhenAuditAppendFails(t *testing.T) {
	auditStore := &failSecondManagedAudit{}
	handler, token, receipts, _ := newManagedClientEvidenceReceiptTestRig(t, auditStore)
	defer receipts.Close()
	body := managedClientBatchJSON("evt_retry", "prompt")

	if rec := postManagedClientBatch(handler, token, body); rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected failed audit append, got %d: %s", rec.Code, rec.Body.String())
	}
	rec := postManagedClientBatch(handler, token, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected retry to succeed after receipt cleanup, got %d: %s", rec.Code, rec.Body.String())
	}
	var response ManagedClientEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if response.Accepted != 1 || response.Duplicate != 0 {
		t.Fatalf("receipt cleanup made retry look duplicate: %#v", response)
	}
}

func TestManagedClientEvidenceRecoversOrphanedReceipt(t *testing.T) {
	innerAudit := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	auditStore := &failSecondRecordingAudit{inner: innerAudit}
	handler, token, receipts, _ := newManagedClientEvidenceReceiptTestRig(t, auditStore)
	defer receipts.Close()
	handler.(*managedClientEvidenceHandler).receipts = &failDeleteOnceReceiptStore{ManagedClientReceiptStore: receipts}
	body := managedClientBatchJSON("evt_retry", "prompt")

	if rec := postManagedClientBatch(handler, token, body); rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected failed audit append, got %d: %s", rec.Code, rec.Body.String())
	}
	rec := postManagedClientBatch(handler, token, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected retry to recover stale receipt, got %d: %s", rec.Code, rec.Body.String())
	}
	var response ManagedClientEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	events, err := innerAudit.EventsBySession(context.Background(), response.SessionID)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	managed := 0
	for _, event := range events {
		if event.ClientEventID == "evt_retry" {
			managed++
		}
	}
	if response.Accepted != 1 || response.Duplicate != 0 || managed != 1 {
		t.Fatalf("expected one recovered audit event, response=%#v events=%#v", response, events)
	}
}

func newManagedClientEvidenceTestRig(t *testing.T) (http.Handler, string, *audit.FileStore, *recordingSessionStore) {
	t.Helper()
	now := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	credStore, err := NewSQLiteDeveloperCredentialStore(":memory:")
	if err != nil {
		t.Fatalf("credential store: %v", err)
	}
	t.Cleanup(func() { _ = credStore.Close() })
	_, token, err := credStore.Issue(context.Background(), DeveloperCredentialIssue{
		ActorSubject: "dev@example.test",
		Client:       "t3code",
		Now:          now,
	})
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}
	auditStore := audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	sessions := &recordingSessionStore{}
	handler := NewManagedClientEvidenceHandler(ManagedClientEvidenceConfig{
		Credentials: credStore,
		Audit:       auditStore,
		Sessions:    sessions,
		Now:         func() time.Time { return now },
		NewID:       fixedIDs("sess_managed_1", "evt_managed_session"),
	})
	return handler, token, auditStore, sessions
}

func newManagedClientEvidenceReceiptTestRig(t *testing.T, auditStore AuditAppender) (http.Handler, string, *SQLitePolicyDecisionStore, *recordingSessionStore) {
	t.Helper()
	now := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)
	credStore, err := NewSQLiteDeveloperCredentialStore(":memory:")
	if err != nil {
		t.Fatalf("credential store: %v", err)
	}
	t.Cleanup(func() { _ = credStore.Close() })
	_, token, err := credStore.Issue(context.Background(), DeveloperCredentialIssue{ActorSubject: "dev@example.test", Client: "t3code", Now: now})
	if err != nil {
		t.Fatalf("issue credential: %v", err)
	}
	receipts, err := NewSQLitePolicyDecisionStore(filepath.Join(t.TempDir(), "governance.db"))
	if err != nil {
		t.Fatalf("receipt store: %v", err)
	}
	sessions := &recordingSessionStore{}
	var idMu sync.Mutex
	nextID := 0
	newID := func(prefix string) string {
		idMu.Lock()
		defer idMu.Unlock()
		nextID++
		return fmt.Sprintf("%s_%d", prefix, nextID)
	}
	handler := NewManagedClientEvidenceHandler(ManagedClientEvidenceConfig{
		Credentials: credStore,
		Audit:       auditStore,
		Sessions:    sessions,
		Receipts:    receipts,
		Now:         func() time.Time { return now },
		NewID:       newID,
	})
	return handler, token, receipts, sessions
}

type failSecondManagedAudit struct {
	mu      sync.Mutex
	appends int
}

type failSecondRecordingAudit struct {
	mu      sync.Mutex
	appends int
	inner   *audit.FileStore
}

func (s *failSecondRecordingAudit) Append(ctx context.Context, event audit.Event) (audit.Event, error) {
	s.mu.Lock()
	s.appends++
	fail := s.appends == 2
	s.mu.Unlock()
	if fail {
		return audit.Event{}, errors.New("audit unavailable")
	}
	return s.inner.Append(ctx, event)
}

func (s *failSecondRecordingAudit) EventsBySession(ctx context.Context, sessionID string) ([]audit.Event, error) {
	return s.inner.EventsBySession(ctx, sessionID)
}

type failDeleteOnceReceiptStore struct {
	ManagedClientReceiptStore
	mu     sync.Mutex
	failed bool
}

func (s *failDeleteOnceReceiptStore) ReleaseManagedClientReceipt(ctx context.Context, receipt ManagedClientReceipt) error {
	s.mu.Lock()
	if !s.failed {
		s.failed = true
		s.mu.Unlock()
		return errors.New("delete unavailable")
	}
	s.mu.Unlock()
	return s.ManagedClientReceiptStore.ReleaseManagedClientReceipt(ctx, receipt)
}

func (s *failSecondManagedAudit) Append(_ context.Context, event audit.Event) (audit.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appends++
	if s.appends == 2 {
		return audit.Event{}, errors.New("audit unavailable")
	}
	return event, nil
}

func managedClientBatchJSON(eventID, eventType string) []byte {
	body, _ := json.Marshal(map[string]any{"events": []map[string]any{{
		"event_id":          eventID,
		"schema_version":    "v0",
		"client":            "t3code",
		"client_session_id": "client_1",
		"event_type":        eventType,
		"timestamp":         "2026-07-02T10:00:00Z",
	}}})
	return body
}

func postManagedClientBatch(handler http.Handler, token string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/managed-client/evidence", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
