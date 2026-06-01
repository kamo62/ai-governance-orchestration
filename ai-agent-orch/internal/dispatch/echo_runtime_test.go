package dispatch

import (
	"context"
	"testing"
)

func TestEchoRuntime_StartSession(t *testing.T) {
	r := NewEchoRuntime()
	ctx := context.Background()
	cfg := SessionConfig{
		SessionID:    "sess_echo",
		ModelID:      "coding-balanced",
		SystemPrompt: "You are a test assistant.",
		UserPrompt:   "Write a test.",
		MCPEndpoints: map[string]string{
			"repo-classification": "http://localhost:8091",
		},
	}

	handle, err := r.StartSession(ctx, cfg)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	var sawPatch, sawDone bool
	for event := range handle.Events() {
		switch event.Type {
		case "patch":
			sawPatch = true
		case "done":
			sawDone = true
		}
	}

	if err := handle.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !sawPatch {
		t.Fatal("expected patch event")
	}
	if !sawDone {
		t.Fatal("expected done event")
	}
}

func TestExtractJSONObject(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"{\"a\":1}", "{\"a\":1}"},
		{"prefix{\"a\":1}suffix", "{\"a\":1}"},
		{"no braces", ""},
		{"{incomplete", ""},
		{"}", ""},
	}
	for _, tt := range tests {
		got := extractJSONObject(tt.input)
		if got != tt.want {
			t.Errorf("extractJSONObject(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizePatchEnvelope_MissingPatchID(t *testing.T) {
	_, ok := normalizePatchEnvelope(`{"files":[{"path":"a.txt","action":"create"}]}`)
	if ok {
		t.Fatal("expected missing patchId to fail")
	}
}

func TestNormalizePatchEnvelope_MissingFiles(t *testing.T) {
	_, ok := normalizePatchEnvelope(`{"patchId":"p1"}`)
	if ok {
		t.Fatal("expected missing files to fail")
	}
}

func TestNormalizePatchEnvelope_EmptyFiles(t *testing.T) {
	_, ok := normalizePatchEnvelope(`{"patchId":"p1","files":[]}`)
	if ok {
		t.Fatal("expected empty files to fail")
	}
}

func TestNormalizePatchEnvelope_AcceptsSnakeCasePatchID(t *testing.T) {
	patch, ok := normalizePatchEnvelope(`{"patch_id":"p1","files":[{"path":"a.txt","action":"create","content":"hello"}]}`)
	if !ok {
		t.Fatal("expected snake_case patch_id to work")
	}
	if !contains(patch, "patchId\":\"p1\"") {
		t.Fatalf("expected normalized patchId, got %s", patch)
	}
}

func contains(s, substr string) bool {
	return containsString(s, substr)
}

func containsString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
