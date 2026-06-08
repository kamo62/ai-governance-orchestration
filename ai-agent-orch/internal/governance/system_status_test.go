package governance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSystemStatusHandlerReturnsGatewayOptions(t *testing.T) {
	handler := NewSystemStatusHandler(SystemStatusConfig{
		Service:               "governance-shell",
		Version:               "v1-test",
		ModelBackend:          "bifrost",
		GatewayAddr:           ":18082",
		RuntimeGatewayEnabled: true,
		ClassificationMax:     "internal",
		PolicyEngine:          "native",
		Gateways: []GatewayOption{
			{ID: "bifrost", Label: "Bifrost", Mode: "sidecar", Default: true},
			{ID: "agentgateway", Label: "AgentGateway", Mode: "sidecar", ComposeFile: "docker-compose.agentgateway.yml"},
			{ID: "native-openrouter", Label: "OpenRouter", Mode: "direct", ComposeFile: "docker-compose.openrouter.yml"},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/system/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Service               string          `json:"service"`
		Version               string          `json:"version"`
		ModelBackend          string          `json:"model_backend"`
		RuntimeGatewayEnabled bool            `json:"runtime_gateway_enabled"`
		Gateways              []GatewayOption `json:"gateways"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Service != "governance-shell" || body.Version != "v1-test" || body.ModelBackend != "bifrost" {
		t.Fatalf("unexpected status body: %#v", body)
	}
	if !body.RuntimeGatewayEnabled {
		t.Fatal("expected runtime gateway to be enabled")
	}
	if len(body.Gateways) != 3 {
		t.Fatalf("expected three gateway options, got %d", len(body.Gateways))
	}
	if body.Gateways[1].ID != "agentgateway" {
		t.Fatalf("expected agentgateway option, got %#v", body.Gateways[1])
	}
}
