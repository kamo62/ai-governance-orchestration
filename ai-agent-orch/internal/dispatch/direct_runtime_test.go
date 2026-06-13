package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
)

func TestOpenRouterReasoningConfigUsesHighEffortFromProviderEnvironment(t *testing.T) {
	t.Setenv("OPENROUTER_REASONING_EFFORT", "high")
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

func TestDirectRuntimeResolvesProviderNativeAlias(t *testing.T) {
	root := t.TempDir()
	writeRegistry(t, root, `
models:
  - alias: bedrock-sonnet
    provider: bedrock
    model_id: anthropic.claude-3-5-sonnet-20240620-v1:0
    fallback_alias: null
`)
	client := &recordingChatClient{
		response: openrouter.ChatCompletionResponse{
			ID:    "gen_test",
			Model: "bedrock/anthropic.claude-3-5-sonnet-20240620-v1:0",
			Choices: []struct {
				Message openrouter.Message `json:"message"`
			}{
				{Message: openrouter.Message{Role: "assistant", Content: "ok"}},
			},
		},
	}
	runtime := NewDirectRuntime(client, root)
	handle, err := runtime.StartSession(context.Background(), SessionConfig{
		SessionID: "sess_direct",
		ModelID:   "bedrock-sonnet",
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	for range handle.Events() {
	}
	if err := handle.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if client.lastRequest.Provider != "bedrock" {
		t.Fatalf("expected provider bedrock, got %q", client.lastRequest.Provider)
	}
	if client.lastRequest.Model != "anthropic.claude-3-5-sonnet-20240620-v1:0" {
		t.Fatalf("unexpected model %q", client.lastRequest.Model)
	}
}

type fakeChatClient struct {
	response openrouter.ChatCompletionResponse
	err      error
}

func (c fakeChatClient) ChatCompletion(context.Context, openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	return c.response, c.err
}

func (c fakeChatClient) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return nil, c.err
}

type recordingChatClient struct {
	response    openrouter.ChatCompletionResponse
	lastRequest openrouter.ChatCompletionRequest
}

func (c *recordingChatClient) ChatCompletion(_ context.Context, req openrouter.ChatCompletionRequest) (openrouter.ChatCompletionResponse, error) {
	c.lastRequest = req
	return c.response, nil
}

func (c *recordingChatClient) ChatCompletionStream(context.Context, openrouter.ChatCompletionRequest) (io.ReadCloser, error) {
	return nil, nil
}

func writeRegistry(t *testing.T, root string, contents string) {
	t.Helper()
	modelsDir := filepath.Join(root, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatalf("mkdir models: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelsDir, "registry.yaml"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

func TestDirectRuntimeFailsRunWhenPerInvocationCostCapExceeded(t *testing.T) {
	client := fakeChatClient{
		response: openrouter.ChatCompletionResponse{
			ID:    "gen_cap",
			Model: "test-provider/model",
			Choices: []struct {
				Message openrouter.Message `json:"message"`
			}{
				{Message: openrouter.Message{Role: "assistant", Content: `{"protocolVersion":1,"patchId":"patch_cap","files":[{"path":"a.md","action":"create","content":"x"}]}`}},
			},
			Usage: openrouter.Usage{TotalTokens: 100, Cost: 0.5},
		},
	}
	runtime := NewDirectRuntime(client, filepath.Join("..", ".."))
	handle, err := runtime.StartSession(context.Background(), SessionConfig{
		SessionID:  "sess_cap",
		ModelID:    "coding-balanced",
		CostCapUSD: 0.1,
	})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	var sawCapError, sawPatch bool
	for event := range handle.Events() {
		if event.Type == "error" && strings.Contains(event.Payload, "cost cap exceeded") {
			sawCapError = true
		}
		if event.Type == "patch" {
			sawPatch = true
		}
	}
	if !sawCapError {
		t.Fatal("expected cost cap error event")
	}
	if sawPatch {
		t.Fatal("expected patch to be withheld after cost cap overrun")
	}
	if err := handle.Wait(); err == nil {
		t.Fatal("expected Wait to report cost cap error")
	}
}

func TestSelectDirectRouteSkipsActorBoundRoutes(t *testing.T) {
	def := catalog.ModelDefinition{
		Alias: "coding-gpt55",
		Routes: []catalog.ModelRoute{
			{Provider: "copilot-user", ModelID: "gpt-5.5", RequiresActorToken: true},
			{Provider: "openrouter", ModelID: "openai/gpt-5.5"},
		},
	}
	route, err := selectDirectRoute(def)
	if err != nil {
		t.Fatalf("selectDirectRoute: %v", err)
	}
	if route.Provider != "openrouter" || route.ModelID != "openai/gpt-5.5" {
		t.Fatalf("expected non-actor route, got %+v", route)
	}
}

func TestSelectDirectRouteFailsWhenOnlyActorBoundRoutesExist(t *testing.T) {
	def := catalog.ModelDefinition{
		Alias: "copilot-only",
		Routes: []catalog.ModelRoute{
			{Provider: "copilot-user", ModelID: "gpt-5.5", RequiresActorToken: true},
		},
	}
	if _, err := selectDirectRoute(def); err == nil {
		t.Fatal("expected error when no non-actor route exists")
	}
}
