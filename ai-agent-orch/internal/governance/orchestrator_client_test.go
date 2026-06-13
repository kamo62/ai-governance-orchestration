package governance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrchestratorHTTPClientSendsServiceBearerToken(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"specialist": "unit-tests",
			"reason":     "testing keyword match",
		})
	}))
	defer server.Close()

	client := NewOrchestratorHTTPClient(server.URL, "local-service-token")
	if _, err := client.Route(t.Context(), "sess_123", "write tests", SessionContext{}); err != nil {
		t.Fatalf("route: %v", err)
	}

	if gotAuth != "Bearer local-service-token" {
		t.Fatalf("expected service auth header, got %q", gotAuth)
	}
}

func TestOrchestratorHTTPClientRouteContextUsesSnakeCaseJSON(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode route body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"specialist": "unit-tests",
			"reason":     "testing keyword match",
		})
	}))
	defer server.Close()

	client := NewOrchestratorHTTPClient(server.URL, "local-service-token")
	_, err := client.Route(t.Context(), "sess_123", "write tests", SessionContext{
		Branch:       "feature/APP-123-login",
		WorkItemID:   "APP-123",
		WorkItemType: "feature",
		SourceSystem: "jira",
	})
	if err != nil {
		t.Fatalf("route: %v", err)
	}

	ctx, ok := body["context"].(map[string]any)
	if !ok {
		t.Fatalf("expected context object, got %#v", body["context"])
	}
	if ctx["branch"] != "feature/APP-123-login" {
		t.Fatalf("expected snake_case branch, got %#v", ctx)
	}
	if _, exists := ctx["Branch"]; exists {
		t.Fatal("expected no PascalCase Branch field in orchestrator payload")
	}
}
