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

func TestPatchBufferSanitizesAndRetrievesPatchContent(t *testing.T) {
	buffer := NewPatchBuffer()

	payload := `{"protocolVersion":1,"patchId":"patch_1","sessionId":"sess_patch","files":[{"path":"tests/example.spec.ts","action":"create","newContent":"test content"}]}`
	sanitized, err := buffer.Store(context.Background(), "sess_patch", payload)
	if err != nil {
		t.Fatalf("store patch: %v", err)
	}
	if strings.Contains(sanitized, "test content") {
		t.Fatalf("sanitized patch leaked content: %s", sanitized)
	}
	if !strings.Contains(sanitized, "proposedContentHash") {
		t.Fatalf("sanitized patch missing content hash: %s", sanitized)
	}

	full, err := buffer.Get(context.Background(), "sess_patch", "patch_1")
	if err != nil {
		t.Fatalf("get patch: %v", err)
	}
	if !strings.Contains(full, "test content") {
		t.Fatalf("full patch missing content: %s", full)
	}
}

func TestPatchFetchHandlerReturnsBufferedPatchContent(t *testing.T) {
	buffer := NewPatchBuffer()
	payload := `{"protocolVersion":1,"patchId":"patch_fetch","sessionId":"sess_fetch","files":[{"path":"tests/example.spec.ts","action":"create","newContent":"test content"}]}`
	if _, err := buffer.Store(context.Background(), "sess_fetch", payload); err != nil {
		t.Fatalf("store patch: %v", err)
	}
	service := NewSessionService(SessionConfig{
		DevToken:    "dev-token",
		Audit:       audit.NewFileStore(filepath.Join(t.TempDir(), "audit.jsonl")),
		PatchBuffer: buffer,
	})
	service.rememberPatch("sess_fetch", "patch_fetch")
	handler := NewPatchFetchHandler(service)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_fetch/patches/patch_fetch", nil)
	req.Header.Set("Authorization", "Bearer dev-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "test content") {
		t.Fatalf("expected buffered patch content, got %s", rec.Body.String())
	}
}

func TestSanitizeRuntimeStreamPayloadRedactsPatchEnvelope(t *testing.T) {
	got := sanitizeRuntimeStreamPayload(`{"patchId":"p1","files":[{"path":"a.txt","action":"create","newContent":"secret-ish content"}]}`)
	if strings.Contains(got, "secret-ish content") {
		t.Fatalf("expected stream sanitizer to redact patch content, got %s", got)
	}
	if got != "Patch proposal received." {
		t.Fatalf("unexpected sanitized payload %q", got)
	}
}

func TestPatchBufferRejectsUnsafePathAndSecrets(t *testing.T) {
	buffer := NewPatchBuffer()

	_, err := buffer.Store(context.Background(), "sess_patch", `{"protocolVersion":1,"patchId":"patch_path","files":[{"path":"../secret.txt","action":"create","newContent":"ok"}]}`)
	if err == nil {
		t.Fatal("expected unsafe path to be rejected")
	}

	_, err = buffer.Store(context.Background(), "sess_patch", `{"protocolVersion":1,"patchId":"patch_secret","files":[{"path":"safe.txt","action":"create","newContent":"OPENROUTER_API_KEY=sk-or-v1-test1234567890"}]}`)
	if err == nil {
		t.Fatal("expected secret content to be rejected")
	}
}

func TestPatchBufferAllowsRemovingSecretFromOriginalContent(t *testing.T) {
	buffer := NewPatchBuffer()

	payload := `{"protocolVersion":1,"patchId":"patch_remediate","sessionId":"sess_patch","files":[{"path":"safe.txt","action":"update","originalContent":"OPENROUTER_API_KEY=sk-or-v1-test1234567890","newContent":"OPENROUTER_API_KEY="}]}`
	sanitized, err := buffer.Store(context.Background(), "sess_patch", payload)
	if err != nil {
		t.Fatalf("expected remediation patch to be allowed, got %v", err)
	}
	if strings.Contains(sanitized, "sk-or-v1") {
		t.Fatalf("sanitized remediation patch leaked original content: %s", sanitized)
	}
}
