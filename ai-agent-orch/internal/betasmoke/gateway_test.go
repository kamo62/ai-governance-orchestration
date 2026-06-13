package betasmoke

import "testing"

func TestExtractAssistantContent(t *testing.T) {
	raw := []byte(`{"choices":[{"message":{"content":"gateway-smoke-ok"}}]}`)
	content, err := extractAssistantContent(raw)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if err := validateExpected(content, "gateway-smoke-ok"); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidateExpectedRejectsMismatch(t *testing.T) {
	if err := validateExpected("other", "gateway-smoke-ok"); err == nil {
		t.Fatal("expected mismatch error")
	}
}
