package orchestrator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-agent-orch/internal/audit"
)

func TestAcceptSessionWritesCorrelatedAuditEvent(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	service := NewSessionIntake(SessionIntakeConfig{
		Audit: audit.NewFileStore(auditPath),
		NewID: func(prefix string) string {
			return "evt_orchestrator_accept_1"
		},
	})
	handler := NewSessionIntakeHandler(service)

	body := []byte(`{"agent":"test-generation"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/sessions", bytes.NewReader(body))
	req.Header.Set("X-AI-Orch-Session-ID", "sess_test_generation_1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var response AcceptSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.SessionID != "sess_test_generation_1" {
		t.Fatalf("unexpected session ID %q", response.SessionID)
	}
	if response.AuditEventID != "evt_orchestrator_accept_1" {
		t.Fatalf("unexpected audit event ID %q", response.AuditEventID)
	}

	auditBytes, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	auditText := string(auditBytes)
	for _, want := range []string{
		`"session_id":"sess_test_generation_1"`,
		`"event_type":"orchestrator.session.accepted"`,
		`"correlation_subject":"orchestrator"`,
	} {
		if !strings.Contains(auditText, want) {
			t.Fatalf("missing %s in audit event: %s", want, auditText)
		}
	}
}

func TestAcceptSessionRequiresCorrelationHeader(t *testing.T) {
	service := NewSessionIntake(SessionIntakeConfig{
		Audit: audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	handler := NewSessionIntakeHandler(service)

	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/sessions", bytes.NewReader([]byte(`{"agent":"test-generation"}`)))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
