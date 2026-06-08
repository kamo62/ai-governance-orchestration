package governance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ai-agent-orch/internal/audit"
)

func TestAuditLookupReturnsSessionEventsWithoutRawPrompt(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	store := audit.NewFileStore(auditPath)
	for _, event := range []audit.Event{
		{
			EventID:           "evt_1",
			SessionID:         "sess_lookup_1",
			EventType:         "session.created",
			PromptSHA256:      "abc123",
			RawPromptStored:   false,
			RawResponseStored: false,
		},
		{
			EventID:       "evt_2",
			SessionID:     "sess_lookup_1",
			EventType:     "model.gateway_call",
			Provider:      "openrouter",
			ModelAlias:    "coding-fast",
			ModelResolved: "openrouter/x-ai/grok-build-0.1",
			TokenUsage: map[string]any{
				"prompt_tokens":     20,
				"completion_tokens": 10,
				"total_tokens":      30,
			},
			RawPromptStored:   false,
			RawResponseStored: false,
		},
		{
			EventID:   "evt_3",
			SessionID: "sess_other",
			EventType: "session.created",
		},
	} {
		if _, err := store.Append(context.Background(), event); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	handler := NewAuditLookupHandler(AuditLookupConfig{
		DevToken: "local-test-token",
		Audit:    store,
		ModelPricing: fakeModelPricingStore{record: ModelPricingRecord{
			Provider:               "openrouter",
			ModelID:                "x-ai/grok-build-0.1",
			PromptCostPerToken:     0.000001,
			CompletionCostPerToken: 0.000002,
		}},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/audit/sessions/sess_lookup_1", nil)
	req.Header.Set("Authorization", "Bearer local-test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"session_id":"sess_lookup_1"`,
		`"event_id":"evt_1"`,
		`"event_id":"evt_2"`,
		`"raw_prompt_stored":false`,
		`"raw_response_stored":false`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in response: %s", want, body)
		}
	}
	if strings.Contains(body, "evt_3") || strings.Contains(body, "raw prompt") {
		t.Fatalf("lookup returned unrelated or raw content: %s", body)
	}
	for _, want := range []string{
		`"model_alias":"coding-fast"`,
		`"prompt_tokens":20`,
		`"completion_tokens":10`,
		`"cost_source":"pricing_table"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing usage summary field %s in response: %s", want, body)
		}
	}
}

func TestAuditLookupRejectsInvalidDevToken(t *testing.T) {
	handler := NewAuditLookupHandler(AuditLookupConfig{
		DevToken: "local-test-token",
		Audit:    audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/audit/sessions/sess_lookup_1", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuditLookupFailsClosedWhenDevTokenMissingEvenWithBearer(t *testing.T) {
	handler := NewAuditLookupHandler(AuditLookupConfig{
		Audit: audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/audit/sessions/sess_lookup_1", nil)
	req.Header.Set("Authorization", "Bearer any-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}
