package dispatch

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ai-agent-orch/internal/openrouter"
)

func TestOpenRouterReasoningConfigUsesHighEffortFromEnvironment(t *testing.T) {
	t.Setenv("AI_ORCH_OPENROUTER_REASONING_EFFORT", "high")
	t.Setenv("AI_ORCH_OPENROUTER_REASONING_EXCLUDE", "false")

	got := openRouterReasoningConfig()
	if got == nil {
		t.Fatal("expected reasoning config")
	}
	if got.Effort != "high" {
		t.Fatalf("expected high effort, got %q", got.Effort)
	}
	if got.Exclude {
		t.Fatal("expected exclude=false from environment")
	}
}

func TestOpenRouterReasoningConfigDefaultsToExcludedReasoning(t *testing.T) {
	t.Setenv("OPENROUTER_REASONING_EFFORT", "high")

	got := openRouterReasoningConfig()
	if got == nil {
		t.Fatal("expected reasoning config")
	}
	if got.Effort != "high" {
		t.Fatalf("expected high effort, got %q", got.Effort)
	}
	if !got.Exclude {
		t.Fatal("expected reasoning exclusion to default to true")
	}
}

func TestDefaultUserPromptIncludesPatchProtocol(t *testing.T) {
	got := defaultUserPrompt("write a smoke test")

	if !strings.Contains(got, "write a smoke test") {
		t.Fatalf("expected user prompt to be preserved, got %q", got)
	}
	for _, required := range []string{"protocolVersion", "patchId", "files", "newContent"} {
		if !strings.Contains(got, required) {
			t.Fatalf("expected prompt to include %q, got %q", required, got)
		}
	}
}

func TestTryExtractPatchNormalizesChangesEnvelope(t *testing.T) {
	handle := &directHandle{}

	got := handle.tryExtractPatch(`{"protocolVersion":1,"patchId":"source_aware_cli_patch","summary":"ok","changes":[{"op":"create","path":"SMOKE_SOURCE_CONTEXT.md","content":"source-aware"}]}`)
	if got == "" {
		t.Fatal("expected patch envelope")
	}

	var patch struct {
		PatchID string `json:"patchId"`
		Files   []struct {
			Path       string `json:"path"`
			Action     string `json:"action"`
			NewContent string `json:"newContent"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(got), &patch); err != nil {
		t.Fatalf("decode normalized patch: %v", err)
	}
	if patch.PatchID != "source_aware_cli_patch" {
		t.Fatalf("patch id = %q", patch.PatchID)
	}
	if len(patch.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(patch.Files))
	}
	if patch.Files[0].Path != "SMOKE_SOURCE_CONTEXT.md" {
		t.Fatalf("path = %q", patch.Files[0].Path)
	}
	if patch.Files[0].Action != "create" {
		t.Fatalf("action = %q", patch.Files[0].Action)
	}
	if patch.Files[0].NewContent != "source-aware" {
		t.Fatalf("new content = %q", patch.Files[0].NewContent)
	}
}

func TestTryExtractPatchRejectsTextWithoutPatchFiles(t *testing.T) {
	handle := &directHandle{}

	if got := handle.tryExtractPatch(`{"patchId":"missing_files","summary":"no file changes"}`); got != "" {
		t.Fatalf("expected no patch, got %s", got)
	}
}

func TestTryExtractPatchAcceptsOperationField(t *testing.T) {
	handle := &directHandle{}

	got := handle.tryExtractPatch(`{"protocolVersion":1,"patchId":"operation_patch","files":[{"operation":"create","path":"SMOKE_SOURCE_CONTEXT.md","content":"source-aware"}]}`)
	if got == "" {
		t.Fatal("expected patch envelope")
	}

	var patch struct {
		Files []struct {
			Action string `json:"action"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(got), &patch); err != nil {
		t.Fatalf("decode normalized patch: %v", err)
	}
	if len(patch.Files) != 1 || patch.Files[0].Action != "create" {
		t.Fatalf("expected create action, got %#v", patch.Files)
	}
}

func TestTryExtractPatchFindsPatchAfterProseObject(t *testing.T) {
	handle := &directHandle{}

	got := handle.tryExtractPatch(`First, here is a non-patch object: {"note":"not the patch"}.

{"protocolVersion":1,"patchId":"patch_after_prose","summary":"ok","files":[{"path":"SMOKE_SOURCE_CONTEXT.md","action":"create","content":"source-aware"}]}`)
	if got == "" {
		t.Fatal("expected patch envelope after prose")
	}
	if !strings.Contains(got, `"patchId":"patch_after_prose"`) {
		t.Fatalf("expected extracted patch, got %s", got)
	}
}

func TestTryExtractPatchAcceptsStringProtocolVersion(t *testing.T) {
	handle := &directHandle{}

	got := handle.tryExtractPatch("```json\n" + `{"protocolVersion":"1.0","patchId":"string_version_patch","summary":"ok","files":[{"path":"SMOKE_SOURCE_CONTEXT.md","action":"create","content":"source-aware"}]}` + "\n```")
	if got == "" {
		t.Fatal("expected patch envelope with string protocol version")
	}
	if !strings.Contains(got, `"protocolVersion":1`) {
		t.Fatalf("expected normalized numeric protocol version, got %s", got)
	}
}

func TestDirectRuntimeDoesNotStreamRawPatchContent(t *testing.T) {
	client := fakeChatClient{
		response: openrouter.ChatCompletionResponse{
			ID:    "gen_test",
			Model: "test-provider/model",
			Choices: []struct {
				Message openrouter.Message `json:"message"`
			}{
				{Message: openrouter.Message{Role: "assistant", Content: `{"protocolVersion":1,"patchId":"patch_raw_stream","files":[{"path":"SMOKE_TEST.md","action":"create","content":"raw patch content"}]}`}},
			},
		},
	}
	runtime := NewDirectRuntime(client, filepath.Join("..", ".."))
	handle, err := runtime.StartSession(context.Background(), SessionConfig{
		SessionID: "sess_direct",
		ModelID:   "coding-balanced",
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	var sawPatch bool
	for event := range handle.Events() {
		if event.Type == "stream" && strings.Contains(event.Payload, "raw patch content") {
			t.Fatalf("stream event leaked raw patch content: %s", event.Payload)
		}
		if event.Type == "patch" {
			sawPatch = true
			if !strings.Contains(event.Payload, "raw patch content") {
				t.Fatalf("patch event should retain transient content before Governance Shell buffering: %s", event.Payload)
			}
		}
	}
	if err := handle.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !sawPatch {
		t.Fatal("expected patch event")
	}
}

type fakeChatClient struct {
	response openrouter.ChatCompletionResponse
	err      error
}

func (c fakeChatClient) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return c.response, c.err
}
