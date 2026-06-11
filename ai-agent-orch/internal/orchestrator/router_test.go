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

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestRouterSelectsSpecialistsForGoldenPrompts(t *testing.T) {
	router := NewRouter(RouterConfig{
		CatalogRoot: filepath.Join("..", ".."),
	})

	cases := []struct {
		prompt string
		want   string
	}{
		{prompt: "Write Playwright tests for this login page.", want: "unit-tests"},
		{prompt: "Review this diff for bugs and risky changes.", want: "code-review"},
		{prompt: "Improve the README for this package.", want: "documentation"},
		{prompt: "Refactor this module without changing behavior.", want: "refactor"},
		{prompt: "Check this code for secret exposure and auth issues.", want: "security-scan"},
		{prompt: "Review the service boundaries and data flow.", want: "architecture-review"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			decision, err := router.SelectSpecialist(tc.prompt, SessionContext{})
			if err != nil {
				t.Fatalf("SelectSpecialist returned error: %v", err)
			}
			if decision.Specialist != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, decision.Specialist)
			}
			if decision.Reason == "" {
				t.Fatalf("expected selection reason")
			}
		})
	}
}

func TestRouterDoesNotMapBroadFeatureBranchToBackend(t *testing.T) {
	router := NewRouter(RouterConfig{
		CatalogRoot: filepath.Join("..", ".."),
	})

	decision, err := router.SelectSpecialist("Review this diff for risky changes.", SessionContext{
		Branch:       "feature/ABC-123-general-work",
		WorkItemType: "feature",
	})
	if err != nil {
		t.Fatalf("SelectSpecialist returned error: %v", err)
	}
	if decision.Specialist == "backend-development" {
		t.Fatalf("feature branches must not blindly route to backend-development: %#v", decision)
	}
	if decision.Specialist != "code-review" {
		t.Fatalf("expected keyword fallback to code-review, got %q", decision.Specialist)
	}
}

func TestRouterUsesSpecificBranchWorkTypes(t *testing.T) {
	router := NewRouter(RouterConfig{
		CatalogRoot: filepath.Join("..", ".."),
	})

	cases := []struct {
		name         string
		branch       string
		workItemType string
		prompt       string
		want         string
	}{
		{name: "frontend prefix", branch: "frontend/ABC-123-nav", workItemType: "frontend", prompt: "Build the navigation UI.", want: "frontend-development"},
		{name: "backend prefix", branch: "backend/ABC-123-api", workItemType: "backend", prompt: "Build the user API.", want: "backend-development"},
		{name: "test prefix", branch: "test/ABC-123-parser", workItemType: "test", prompt: "Add coverage.", want: "unit-tests"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := router.SelectSpecialist(tc.prompt, SessionContext{
				Branch:       tc.branch,
				WorkItemType: tc.workItemType,
			})
			if err != nil {
				t.Fatalf("SelectSpecialist returned error: %v", err)
			}
			if decision.Specialist != tc.want {
				t.Fatalf("expected %q, got %q (%s)", tc.want, decision.Specialist, decision.Reason)
			}
		})
	}
}

func TestRouteEndpointWritesAuditWithoutRawPrompt(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	router := NewRouter(RouterConfig{
		CatalogRoot: filepath.Join("..", ".."),
		Audit:       audit.NewFileStore(auditPath),
		NewID: func(prefix string) string {
			return "evt_router_selected_1"
		},
	})
	handler := NewRouterHandler(router)

	body := []byte(`{"prompt":"Write Playwright tests for a checkout flow."}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/route", bytes.NewReader(body))
	req.Header.Set("X-AI-Orch-Session-ID", "sess_router_1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response RouteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.SessionID != "sess_router_1" {
		t.Fatalf("unexpected session ID %q", response.SessionID)
	}
	if response.Specialist != "unit-tests" {
		t.Fatalf("expected unit-tests, got %q", response.Specialist)
	}
	if response.AuditEventID != "evt_router_selected_1" {
		t.Fatalf("unexpected audit event ID %q", response.AuditEventID)
	}

	auditBytes, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	auditText := string(auditBytes)
	for _, want := range []string{
		`"session_id":"sess_router_1"`,
		`"event_type":"router.specialist.selected"`,
		`"agent":"unit-tests"`,
		`"correlation_subject":"orchestrator"`,
	} {
		if !strings.Contains(auditText, want) {
			t.Fatalf("missing %s in audit event: %s", want, auditText)
		}
	}
	if strings.Contains(auditText, "checkout flow") {
		t.Fatalf("router audit event must not store raw prompt: %s", auditText)
	}
}

func TestRouteEndpointRequiresSessionCorrelationHeader(t *testing.T) {
	router := NewRouter(RouterConfig{
		CatalogRoot: filepath.Join("..", ".."),
		Audit:       audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
	})
	handler := NewRouterHandler(router)

	req := httptest.NewRequest(http.MethodPost, "/v1/orchestrator/route", bytes.NewReader([]byte(`{"prompt":"Write tests"}`)))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
