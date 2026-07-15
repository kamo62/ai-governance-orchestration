package modelgateway

import (
	"net/http/httptest"
	"testing"
)

func TestRuntimeAuthAcceptsDeveloperCredentialAndReturnsBoundActor(t *testing.T) {
	gateway := NewGateway(GatewayConfig{
		RuntimeToken: "shared-runtime",
		RuntimeCredentialValidator: func(token string) (string, bool) {
			if token == "air_devtoken" {
				return "dev@example.test", true
			}
			return "", false
		},
	})
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer air_devtoken")

	actor, ok := gateway.runtimeAuth(req)
	if !ok || actor != "dev@example.test" {
		t.Fatalf("expected developer credential actor, ok=%t actor=%q", ok, actor)
	}
}

func TestModelListContextPrefersCredentialBoundActorOverSpoofedHeader(t *testing.T) {
	gateway := NewGateway(GatewayConfig{
		RuntimeToken: "shared-runtime",
		RuntimeCredentialValidator: func(token string) (string, bool) {
			if token == "air_devtoken" {
				return "dev@example.test", true
			}
			return "", false
		},
	})
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer air_devtoken")
	req.Header.Set("X-AI-Orch-Actor-Subject", "other@example.test")

	_, actor := gateway.modelListContext(req)
	if actor != "dev@example.test" {
		t.Fatalf("expected credential-bound actor to win, got %q", actor)
	}
}
