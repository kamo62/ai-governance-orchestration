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
	"strings"
	"testing"

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
		"agent": "test-generation",
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

	body := []byte(`{"agent":"test-generation","classification":"internal","prompt":"raw secret prompt must not be read"}`)
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

	req := authorizedSessionRequest(`{"agent":"test-generation","classification":"internal","prompt":"kill switch raw prompt must not be read"}`)
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

	req := authorizedSessionRequest(`{"agent":"test-generation","classification":"restricted","prompt":"restricted repo details must not dispatch"}`)
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
	req := authorizedSessionRequest(fmt.Sprintf(`{"agent":"test-generation","classification":"internal","prompt":"use OPENROUTER_API_KEY=%s for this run"}`, fakeToken))
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

	req := authorizedSessionRequest(`{"agent":"test-generation","classification":"internal","prompt":"ordinary prompt","estimated_cost_usd":0.30}`)
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

	req := authorizedSessionRequest(`{"agent":"test-generation","classification":"internal","prompt":"ordinary prompt","estimated_cost_usd":1.25}`)
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

	req := authorizedSessionRequest(`{"agent":"test-generation","classification":"internal","prompt":"normal prompt"}`)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sess_should_not_escape") {
		t.Fatalf("session ID must not be returned when audit write fails: %s", rec.Body.String())
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
