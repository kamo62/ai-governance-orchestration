package governance

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestEndToEndSessionFlow(t *testing.T) {
	// 1. Setup services.
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	auditStore := audit.NewFileStore(auditPath)
	sessionService := NewSessionService(SessionConfig{
		DevToken:          "local-test-token",
		Audit:             auditStore,
		ClassificationMax: "internal",
		NewID:             fixedIDs("sess_e2e_1", "evt_create_1", "evt_route_1", "evt_confirm_1", "evt_dispatch_1", "evt_patch_1"),
	})
	fakeOrch := &fakeOrchestrator{
		specialist: "unit-tests",
		reason:     "testing keyword match",
	}
	eventStore := NewEventStore()

	// 2. Create session.
	handler := http.NewServeMux()
	handler.Handle("/v1/sessions", NewSessionHandler(sessionService))
	handler.Handle("/v1/sessions/", &testSubrouter{
		sessionService: sessionService,
		orchClient:     fakeOrch,
		events:         eventStore,
	})
	handler.Handle("/v1/audit/sessions/", NewAuditLookupHandler(AuditLookupConfig{
		DevToken: "local-test-token",
		Audit:    auditStore,
	}))

	// POST /v1/sessions
	body := []byte(`{"agent":"unit-tests","classification":"internal","prompt":"write tests for login"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create session: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	sessionID := createResp.SessionID
	if sessionID == "" {
		t.Fatal("session ID not returned")
	}

	// 3. Send message (routing).
	body = []byte(`{"prompt":"write tests for login"}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("send message: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var routeResp struct {
		Specialist string `json:"specialist"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &routeResp); err != nil {
		t.Fatalf("decode route response: %v", err)
	}
	if routeResp.Specialist != "unit-tests" {
		t.Fatalf("expected unit-tests, got %s", routeResp.Specialist)
	}
	if routeResp.Status != "awaiting_confirmation" {
		t.Fatalf("expected awaiting_confirmation, got %s", routeResp.Status)
	}

	// 4. Confirm specialist.
	body = []byte(`{"agent":"unit-tests"}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/confirm", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. SSE events should be published.
	// Wait a moment for the goroutine to publish events.
	time.Sleep(100 * time.Millisecond)

	ch := eventStore.Subscribe(sessionID)
	defer eventStore.Unsubscribe(sessionID, ch)

	var eventTypes []string
	done := time.After(2 * time.Second)
loop:
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				break loop
			}
			eventTypes = append(eventTypes, event.Type)
			if event.Type == "done" {
				break loop
			}
		case <-done:
			break loop
		}
	}

	if len(eventTypes) == 0 {
		t.Fatal("expected events from stream, got none")
	}
	foundStream := false
	foundDone := false
	for _, et := range eventTypes {
		if et == "stream" {
			foundStream = true
		}
		if et == "done" {
			foundDone = true
		}
	}
	if !foundStream {
		t.Fatalf("expected stream event, got: %v", eventTypes)
	}
	if !foundDone {
		t.Fatalf("expected done event, got: %v", eventTypes)
	}
	if fakeOrch.dispatchPrompt != "write tests for login" {
		t.Fatalf("expected dispatch prompt to be preserved, got %q", fakeOrch.dispatchPrompt)
	}

	// 6. Patch decision.
	body = []byte(`{"patch_id":"patch_1","decision":"applied"}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/patch-decision", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-test-token")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("patch decision: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// 7. Audit lookup should have 4 events: created, router, confirmed, patch.
	req = httptest.NewRequest(http.MethodGet, "/v1/audit/sessions/"+sessionID, nil)
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("audit lookup: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var auditResp struct {
		SessionID string        `json:"session_id"`
		Events    []audit.Event `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &auditResp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(auditResp.Events) < 3 {
		t.Fatalf("expected at least 3 audit events, got %d: %+v", len(auditResp.Events), auditResp.Events)
	}

	// Verify no raw prompt stored.
	auditText := readAuditText(t, auditPath)
	if strings.Contains(auditText, "write tests for login") {
		t.Fatal("audit must not contain raw prompt")
	}
}

// testSubrouter is a simplified subrouter for integration tests.
type testSubrouter struct {
	sessionService *SessionService
	orchClient     OrchestratorClient
	events         *EventStore
}

func (sr *testSubrouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/messages"):
		NewMessagesHandler(sr.sessionService, sr.orchClient).ServeHTTP(w, r)
	case strings.HasSuffix(path, "/confirm"):
		NewConfirmHandlerWithEvents(sr.sessionService, sr.orchClient, sr.events).ServeHTTP(w, r)
	case strings.HasSuffix(path, "/patch-decision"):
		NewPatchDecisionHandler(sr.sessionService).ServeHTTP(w, r)
	case strings.HasSuffix(path, "/events"):
		NewEventsHandler(sr.events).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}
