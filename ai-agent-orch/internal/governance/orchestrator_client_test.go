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
			"specialist": "test-generation",
			"reason":     "testing keyword match",
		})
	}))
	defer server.Close()

	client := NewOrchestratorHTTPClient(server.URL, "local-service-token")
	if _, err := client.Route(t.Context(), "sess_123", "write tests"); err != nil {
		t.Fatalf("route: %v", err)
	}

	if gotAuth != "Bearer local-service-token" {
		t.Fatalf("expected service auth header, got %q", gotAuth)
	}
}
