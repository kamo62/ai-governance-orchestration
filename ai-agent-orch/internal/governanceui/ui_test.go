package governanceui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesEmbeddedIndex(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "AI Orch Governance") {
		t.Fatalf("expected UI title in response, got %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "CIO Demo Readiness") {
		t.Fatalf("expected readiness panel in response, got %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Recent Sessions") || !strings.Contains(rec.Body.String(), "Session Audit Trail") {
		t.Fatalf("expected session/audit panels in response, got %q", rec.Body.String())
	}
}

func TestRedirectSendsUiSlash(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ui", nil)
	rec := httptest.NewRecorder()

	Redirect().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/ui/" {
		t.Fatalf("expected /ui/ redirect, got %q", rec.Header().Get("Location"))
	}
}
