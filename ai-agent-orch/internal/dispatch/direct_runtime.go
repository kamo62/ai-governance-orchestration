package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/catalog"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
	patchproto "github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/patch"
)

// DirectRuntime calls OpenRouter directly without an ACP subprocess.
// It is the Phase 1 fallback when OpenCode is not installed.
type DirectRuntime struct {
	client      openrouter.ChatClient
	catalogRoot string
}

func NewDirectRuntime(client openrouter.ChatClient, catalogRoot string) *DirectRuntime {
	return &DirectRuntime{
		client:      client,
		catalogRoot: catalogRoot,
	}
}

func (r *DirectRuntime) StartSession(ctx context.Context, cfg SessionConfig) (SessionHandle, error) {
	modelDef, err := catalog.ResolveModelDefinition(r.catalogRoot, cfg.ModelID)
	if err != nil {
		return nil, fmt.Errorf("resolve model %q: %w", cfg.ModelID, err)
	}
	route, err := selectDirectRoute(modelDef)
	if err != nil {
		return nil, fmt.Errorf("route model %q: %w", cfg.ModelID, err)
	}

	handle := &directHandle{
		client:   r.client,
		config:   cfg,
		provider: route.Provider,
		modelID:  route.ModelID,
		events:   make(chan RuntimeEvent, 16),
		done:     make(chan struct{}),
	}

	go handle.run(ctx)
	return handle, nil
}

// selectDirectRoute picks the first catalog route this lane can serve. The
// direct runtime holds no per-actor credentials, so actor-bound routes (for
// example copilot-user) are skipped rather than silently rewritten to the
// model's top-level defaults.
func selectDirectRoute(modelDef catalog.ModelDefinition) (catalog.ModelRoute, error) {
	for _, route := range modelDef.EffectiveRoutes() {
		if route.RequiresActorToken {
			continue
		}
		if strings.TrimSpace(route.Provider) == "" || strings.TrimSpace(route.ModelID) == "" {
			continue
		}
		return route, nil
	}
	return catalog.ModelRoute{}, fmt.Errorf("alias %q has no route usable without an actor token", modelDef.Alias)
}

type directHandle struct {
	client   openrouter.ChatClient
	config   SessionConfig
	provider string
	modelID  string
	events   chan RuntimeEvent
	done     chan struct{}
	err      error
}

func (h *directHandle) run(ctx context.Context) {
	defer close(h.done)
	defer close(h.events)

	systemPrompt := h.config.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful coding assistant. Respond with code changes in a clear format."
	}

	h.events <- RuntimeEvent{
		Type:    "stream",
		Payload: "Starting direct runtime session...",
	}

	// Simulate MCP tool calls for each configured endpoint.
	for name, url := range h.config.MCPEndpoints {
		h.events <- RuntimeEvent{
			Type:    "mcp_call",
			Payload: fmt.Sprintf("Calling MCP %s at %s", name, url),
		}
	}

	req := openrouter.ChatCompletionRequest{
		SessionID:  h.config.SessionID,
		ModelAlias: h.config.ModelID,
		Provider:   h.provider,
		Model:      h.modelID,
		Messages: []openrouter.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: defaultUserPrompt(h.config.UserPrompt)},
		},
		Temperature: 0.2,
		MaxTokens:   4096,
	}
	if reasoning := openRouterReasoningConfig(); reasoning != nil {
		req.Reasoning = reasoning
	}

	resp, err := h.client.ChatCompletion(ctx, req)
	if err != nil {
		h.err = err
		h.events <- RuntimeEvent{
			Type:    "error",
			Payload: fmt.Sprintf("OpenRouter call failed: %v", err),
		}
		return
	}

	content := resp.FirstContent()
	patch := h.tryExtractPatch(content)
	usagePayload, _ := json.Marshal(map[string]any{
		"model":             resp.Model,
		"requested_model":   h.modelID,
		"reasoning_effort":  reasoningEffort(),
		"prompt_tokens":     resp.Usage.PromptTokens,
		"completion_tokens": resp.Usage.CompletionTokens,
		"reasoning_tokens":  resp.Usage.CompletionTokensDetails.ReasoningTokens,
		"total_tokens":      resp.Usage.TotalTokens,
		"cost_usd":          resp.Usage.Cost,
	})
	h.events <- RuntimeEvent{
		Type:    "model_usage",
		Payload: string(usagePayload),
	}

	// Enforce the agent's per-invocation cost cap. Provider cost is only
	// known after the call, so an overrun fails the run and withholds the
	// work product instead of letting it flow into patch review.
	if h.config.CostCapUSD > 0 && resp.Usage.Cost > h.config.CostCapUSD {
		h.err = fmt.Errorf("per-invocation cost cap exceeded: %.6f USD > %.6f USD cap", resp.Usage.Cost, h.config.CostCapUSD)
		h.events <- RuntimeEvent{
			Type:    "error",
			Payload: h.err.Error(),
		}
		return
	}

	h.events <- RuntimeEvent{
		Type:    "stream",
		Payload: directStreamPayload(content, patch),
	}

	// Try to extract a patch envelope from the response.
	if patch != "" {
		h.events <- RuntimeEvent{
			Type:    "patch",
			Payload: patch,
		}
	}

	h.events <- RuntimeEvent{
		Type:    "done",
		Payload: fmt.Sprintf("session complete (%d tokens)", resp.Usage.TotalTokens),
	}
}

func directStreamPayload(content string, patch string) string {
	if patch == "" {
		return content
	}
	return "Patch proposal received."
}

func (h *directHandle) tryExtractPatch(content string) string {
	for _, raw := range extractJSONObjects(content) {
		patch, ok := normalizePatchEnvelope(raw)
		if ok {
			return patch
		}
	}
	return ""
}

func extractJSONObjects(content string) []string {
	var objects []string
	for start := strings.Index(content, "{"); start >= 0; {
		var raw json.RawMessage
		decoder := json.NewDecoder(strings.NewReader(content[start:]))
		if err := decoder.Decode(&raw); err == nil && len(raw) > 0 && raw[0] == '{' {
			objects = append(objects, string(raw))
		}
		next := strings.Index(content[start+1:], "{")
		if next < 0 {
			break
		}
		start += next + 1
	}
	return objects
}

type modelPatchEnvelope struct {
	ProtocolVersion json.RawMessage  `json:"protocolVersion"`
	PatchID         string           `json:"patchId"`
	PatchIDSnake    string           `json:"patch_id"`
	SessionID       string           `json:"sessionId"`
	Summary         string           `json:"summary"`
	Rationale       string           `json:"rationale"`
	Files           []modelPatchFile `json:"files"`
	Changes         []modelPatchFile `json:"changes"`
}

type modelPatchFile struct {
	Path                string `json:"path"`
	Action              string `json:"action"`
	Op                  string `json:"op"`
	Operation           string `json:"operation"`
	OriginalContentHash string `json:"originalContentHash"`
	ProposedContentHash string `json:"proposedContentHash"`
	OriginalContent     string `json:"originalContent"`
	NewContent          string `json:"newContent"`
	Content             string `json:"content"`
}

func normalizePatchEnvelope(raw string) (string, bool) {
	var in modelPatchEnvelope
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return "", false
	}

	patchID := in.PatchID
	if patchID == "" {
		patchID = in.PatchIDSnake
	}
	if patchID == "" {
		return "", false
	}

	files := in.Files
	if len(files) == 0 {
		files = in.Changes
	}
	if len(files) == 0 {
		return "", false
	}

	version := protocolVersion(in.ProtocolVersion)
	if version == 0 {
		version = 1
	}

	out := patchproto.PatchEnvelope{
		ProtocolVersion: version,
		PatchID:         patchID,
		SessionID:       in.SessionID,
		Summary:         in.Summary,
		Rationale:       in.Rationale,
		Files:           make([]patchproto.PatchFile, 0, len(files)),
	}
	for _, file := range files {
		action := file.Action
		if action == "" {
			action = file.Op
		}
		if action == "" {
			action = file.Operation
		}
		if file.Path == "" || action == "" {
			continue
		}
		newContent := file.NewContent
		if newContent == "" {
			newContent = file.Content
		}
		out.Files = append(out.Files, patchproto.PatchFile{
			Path:                file.Path,
			Action:              action,
			OriginalContentHash: file.OriginalContentHash,
			ProposedContentHash: file.ProposedContentHash,
			OriginalContent:     file.OriginalContent,
			NewContent:          newContent,
		})
	}
	if len(out.Files) == 0 {
		return "", false
	}

	normalized, err := json.Marshal(out)
	if err != nil {
		return "", false
	}
	return string(normalized), true
}

func protocolVersion(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		return asInt
	}
	var asFloat float64
	if err := json.Unmarshal(raw, &asFloat); err == nil {
		return int(asFloat)
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		trimmed := strings.TrimSpace(strings.TrimPrefix(asString, "v"))
		if n, err := strconv.Atoi(trimmed); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int(f)
		}
	}
	return 0
}

func openRouterReasoningConfig() *openrouter.ReasoningConfig {
	effort := reasoningEffort()
	if effort == "" {
		return nil
	}
	return &openrouter.ReasoningConfig{
		Effort:  effort,
		Exclude: envBoolDefault("AI_ORCH_OPENROUTER_REASONING_EXCLUDE", true),
	}
}

func reasoningEffort() string {
	return os.Getenv("OPENROUTER_REASONING_EFFORT")
}

func envBoolDefault(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch value {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func (h *directHandle) Wait() error {
	<-h.done
	return h.err
}

func (h *directHandle) Events() <-chan RuntimeEvent {
	return h.events
}

func (h *directHandle) Stop() error {
	return nil
}
