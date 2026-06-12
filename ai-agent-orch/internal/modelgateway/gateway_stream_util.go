package modelgateway

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/modelbackend"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/router"
)

func startChatStream(ctx context.Context, backend modelbackend.Backend, decision router.Decision, req openAIChatCompletionRequest, actorSubject string, body []byte) (io.ReadCloser, error) {
	if rawBackend, ok := backend.(modelbackend.RawChatBackend); ok {
		return rawBackend.ChatCompletionStreamRaw(ctx, modelbackend.RawRequest{
			Provider:     decision.Provider,
			ModelAlias:   decision.SelectedAlias,
			Model:        decision.SelectedModelID,
			Body:         body,
			ActorSubject: actorSubject,
		})
	}
	upstream := openrouter.ChatCompletionRequest{
		Provider:      decision.Provider,
		ModelAlias:    decision.SelectedAlias,
		Model:         decision.SelectedModelID,
		Messages:      convertMessages(req.Messages),
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		Stream:        true,
		StreamOptions: &openrouter.StreamOptions{IncludeUsage: true},
	}
	if decision.ReasoningSupportsEffort && decision.ReasoningEffortApplied != "" {
		upstream.Reasoning = &openrouter.ReasoningConfig{Effort: decision.ReasoningEffortApplied}
	}
	return backend.ChatCompletionStream(ctx, upstream)
}

func ensureChatStreamOptions(body []byte) []byte {
	body = ensureJSONBool(body, "stream", true)
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	if _, ok := obj["stream_options"]; !ok {
		obj["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	} else {
		var opts map[string]json.RawMessage
		if err := json.Unmarshal(obj["stream_options"], &opts); err == nil {
			opts["include_usage"] = json.RawMessage(`true`)
			if encoded, err := json.Marshal(opts); err == nil {
				obj["stream_options"] = encoded
			}
		}
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return encoded
}

func ensureJSONBool(body []byte, key string, value bool) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	if value {
		obj[key] = json.RawMessage(`true`)
	} else {
		obj[key] = json.RawMessage(`false`)
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return encoded
}

func rewriteTopLevelModel(body []byte, alias string) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	model, err := json.Marshal(alias)
	if err != nil {
		return body
	}
	obj["model"] = model
	encoded, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return encoded
}

func rewriteSSELine(line string, alias string) (string, bool, map[string]any) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "data:") {
		payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if payload == "[DONE]" {
			return line, true, nil
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			return line, false, nil
		}
		if _, ok := obj["model"]; ok {
			if model, err := json.Marshal(alias); err == nil {
				obj["model"] = model
			}
		}
		usage := usageFromRawObject(obj)
		encoded, err := json.Marshal(obj)
		if err != nil {
			return line, false, usage
		}
		return "data: " + string(encoded), false, usage
	}
	return line, false, nil
}

func usageFromRawResponse(body []byte) map[string]any {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil
	}
	return usageFromRawObject(obj)
}

func usageFromRawObject(obj map[string]json.RawMessage) map[string]any {
	var usage map[string]any
	if usageRaw, ok := obj["usage"]; ok {
		usage = usageMapFromRaw(usageRaw)
	}
	// Copilot reports AI-unit consumption outside the standard usage block.
	if copilotRaw, ok := obj["copilot_usage"]; ok {
		var copilotUsage map[string]any
		if err := json.Unmarshal(copilotRaw, &copilotUsage); err == nil {
			if v, numOK := numericValue(copilotUsage["total_nano_aiu"]); numOK {
				if usage == nil {
					usage = make(map[string]any)
				}
				usage["copilot_nano_aiu"] = v
			}
		}
	}
	if usage != nil {
		return usage
	}
	if responseRaw, ok := obj["response"]; ok {
		var response map[string]json.RawMessage
		if err := json.Unmarshal(responseRaw, &response); err == nil {
			return usageFromRawObject(response)
		}
	}
	return nil
}

func usageMapFromRaw(raw json.RawMessage) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	usage := make(map[string]any)
	copyUsageNumber(usage, parsed, "prompt_tokens")
	copyUsageNumber(usage, parsed, "completion_tokens")
	copyUsageNumber(usage, parsed, "total_tokens")
	copyUsageNumber(usage, parsed, "input_tokens")
	copyUsageNumber(usage, parsed, "output_tokens")
	if _, ok := usage["total_tokens"]; !ok {
		if input, inputOK := numericValue(parsed["input_tokens"]); inputOK {
			if output, outputOK := numericValue(parsed["output_tokens"]); outputOK {
				usage["total_tokens"] = input + output
			}
		}
	}
	copyUsageCost(usage, parsed, "cost")
	copyUsageCost(usage, parsed, "cost_usd")
	copyNestedUsageNumber(usage, parsed, "prompt_tokens_details", "cached_tokens", "cache_read_tokens")
	copyNestedUsageNumber(usage, parsed, "input_tokens_details", "cached_tokens", "cache_read_tokens")
	copyNestedUsageNumber(usage, parsed, "completion_tokens_details", "reasoning_tokens", "reasoning_tokens")
	copyNestedUsageNumber(usage, parsed, "output_tokens_details", "reasoning_tokens", "reasoning_tokens")
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func copyUsageNumber(dst map[string]any, src map[string]any, key string) {
	if value, ok := numericValue(src[key]); ok {
		dst[key] = value
	}
}

func copyNestedUsageNumber(dst map[string]any, src map[string]any, parent string, key string, dstKey string) {
	obj, ok := src[parent].(map[string]any)
	if !ok {
		return
	}
	if value, ok := numericValue(obj[key]); ok {
		dst[dstKey] = value
	}
}

func copyUsageCost(dst map[string]any, src map[string]any, key string) {
	value, ok := src[key]
	if !ok {
		return
	}
	if n, ok := numericValue(value); ok {
		dst["cost_usd"] = n
		return
	}
	if s, ok := value.(string); ok {
		if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			dst["cost_usd"] = n
		}
	}
}

func numericValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}
