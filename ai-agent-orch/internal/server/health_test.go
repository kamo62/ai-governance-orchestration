package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthzReturnsServiceStatus(t *testing.T) {
	srv := New("governance-shell", func() error { return nil }, func() (CatalogSummary, error) {
		return CatalogSummary{Agents: 7, Models: 5}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"service":"governance-shell"`) {
		t.Fatalf("missing service name in response: %s", rec.Body.String())
	}
}

func TestReadyzReturnsUnavailableWhenCatalogInvalid(t *testing.T) {
	srv := New("orchestrator", func() error { return errCatalogInvalid{} }, nil)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestCatalogSummaryEndpoint(t *testing.T) {
	srv := New("orchestrator", func() error { return nil }, func() (CatalogSummary, error) {
		return CatalogSummary{Agents: 7, Models: 5}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/catalog/summary", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"agents":7`) || !strings.Contains(rec.Body.String(), `"models":5`) {
		t.Fatalf("unexpected summary response: %s", rec.Body.String())
	}
}

type errCatalogInvalid struct{}

func (errCatalogInvalid) Error() string { return "catalog invalid" }
