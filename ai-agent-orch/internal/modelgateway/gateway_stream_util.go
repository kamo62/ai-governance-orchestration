package modelgateway

import (
	"bufio"
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
		responsesBackend, responsesOK := backend.(modelbackend.RawResponsesBackend)
		if responsesOK && copilotModelUsesResponsesAPI(decision.Provider, decision.SelectedModelID) {
			return startResponsesStreamAsChatCompletion(ctx, responsesBackend, decision, req, actorSubject, body, nil)
		}
		stream, err := rawBackend.ChatCompletionStreamRaw(ctx, modelbackend.RawRequest{
			Provider:     decision.Provider,
			ModelAlias:   decision.SelectedAlias,
			Model:        decision.SelectedModelID,
			Body:         body,
			ActorSubject: actorSubject,
		})
		if err == nil {
			return stream, nil
		}
		if !responsesOK || !responsesOnlyChatError(err) {
			return nil, err
		}
		return startResponsesStreamAsChatCompletion(ctx, responsesBackend, decision, req, actorSubject, body, err)
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

func responsesOnlyChatError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unsupported_api_for_model") ||
		strings.Contains(message, "not accessible via the /chat/completions endpoint")
}

func copilotModelUsesResponsesAPI(provider string, model string) bool {
	provider = strings.TrimSpace(strings.ToLower(provider))
	if provider != modelbackend.BackendCopilotUser && provider != "github-copilot" {
		return false
	}
	model = strings.TrimSpace(strings.ToLower(model))
	if !strings.HasPrefix(model, "gpt-") {
		return false
	}
	versionPart := strings.TrimPrefix(model, "gpt-")
	end := 0
	for end < len(versionPart) && versionPart[end] >= '0' && versionPart[end] <= '9' {
		end++
	}
	if end == 0 {
		return false
	}
	major, err := strconv.Atoi(versionPart[:end])
	if err != nil || major < 5 {
		return false
	}
	return !strings.HasPrefix(model, "gpt-5-mini")
}

func responsesRawAsChatCompletion(ctx context.Context, responsesBackend modelbackend.RawResponsesBackend, decision router.Decision, req openAIChatCompletionRequest, actorSubject string, chatBody []byte) ([]byte, map[string]any, error) {
	responsesBody, err := chatCompletionToResponsesBody(req, decision.SelectedAlias, chatBody)
	if err != nil {
		return nil, nil, err
	}
	responsesBody = ensureJSONBool(responsesBody, "stream", false)
	respBody, err := responsesBackend.ResponsesRaw(ctx, modelbackend.RawRequest{
		Provider:     decision.Provider,
		ModelAlias:   decision.SelectedAlias,
		Model:        decision.SelectedModelID,
		Body:         responsesBody,
		ActorSubject: actorSubject,
	})
	if err != nil {
		return nil, nil, err
	}
	usage := usageFromRawResponse(respBody)
	chatBodyOut, err := responsesRawToChatCompletion(respBody, decision.SelectedAlias)
	if err != nil {
		return nil, nil, err
	}
	return chatBodyOut, usage, nil
}

func responsesRawToChatCompletion(body []byte, alias string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	id := rawString(obj["id"])
	if id == "" {
		id = "chatcmpl_ai_orch_responses_bridge"
	}
	content := responsesOutputText(obj["output"])
	usage := normalizeChatCompletionUsage(usageFromRawObject(obj))
	resp := map[string]any{
		"id":     id,
		"object": "chat.completion",
		"model":  alias,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": usage,
	}
	return json.Marshal(resp)
}

func responsesOutputText(raw json.RawMessage) string {
	var items []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var parts []string
	for _, item := range items {
		if rawString(item["type"]) != "message" {
			continue
		}
		var contentItems []map[string]json.RawMessage
		if len(item["content"]) == 0 || json.Unmarshal(item["content"], &contentItems) != nil {
			continue
		}
		for _, content := range contentItems {
			if rawString(content["type"]) == "output_text" {
				if text := rawString(content["text"]); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, "")
}

func startResponsesStreamAsChatCompletion(ctx context.Context, responsesBackend modelbackend.RawResponsesBackend, decision router.Decision, req openAIChatCompletionRequest, actorSubject string, chatBody []byte, fallbackErr error) (io.ReadCloser, error) {
	responsesBody, convertErr := chatCompletionToResponsesBody(req, decision.SelectedAlias, chatBody)
	if convertErr != nil {
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		return nil, convertErr
	}
	responsesStream, responsesErr := responsesBackend.ResponsesStreamRaw(ctx, modelbackend.RawRequest{
		Provider:     decision.Provider,
		ModelAlias:   decision.SelectedAlias,
		Model:        decision.SelectedModelID,
		Body:         responsesBody,
		ActorSubject: actorSubject,
	})
	if responsesErr != nil {
		return nil, responsesErr
	}
	return responsesStreamAsChatCompletionStream(ctx, responsesStream, decision.SelectedAlias), nil
}

func chatCompletionToResponsesBody(req openAIChatCompletionRequest, alias string, chatBody []byte) ([]byte, error) {
	if len(chatBody) > 0 {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(chatBody, &raw); err == nil {
			return rawChatCompletionToResponsesBody(req, alias, raw)
		}
	}
	return parsedChatCompletionToResponsesBody(req, alias)
}

func parsedChatCompletionToResponsesBody(req openAIChatCompletionRequest, alias string) ([]byte, error) {
	input := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		input = append(input, map[string]any{
			"role":    role,
			"content": msg.Content.String(),
		})
	}
	body := map[string]any{
		"model":  alias,
		"input":  input,
		"stream": true,
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != 0 {
		body["temperature"] = req.Temperature
	}
	return json.Marshal(body)
}

func rawChatCompletionToResponsesBody(req openAIChatCompletionRequest, alias string, raw map[string]json.RawMessage) ([]byte, error) {
	body := map[string]any{
		"model":  alias,
		"input":  responsesInputFromRawChatMessages(raw["messages"], req.Messages),
		"stream": true,
	}
	copyRawJSONValueAs(body, raw, "max_tokens", "max_output_tokens")
	copyRawJSONValueAs(body, raw, "max_completion_tokens", "max_output_tokens")
	copyRawJSONValue(body, raw, "temperature")
	copyRawJSONValue(body, raw, "top_p")
	copyRawJSONValue(body, raw, "user")
	copyRawJSONValue(body, raw, "metadata")
	copyRawJSONValue(body, raw, "parallel_tool_calls")
	if tools := responsesToolsFromRawChatTools(raw["tools"]); len(tools) > 0 {
		body["tools"] = tools
	}
	if toolChoice, ok := responsesToolChoiceFromRaw(raw["tool_choice"]); ok {
		body["tool_choice"] = toolChoice
	}
	return json.Marshal(body)
}

func responsesInputFromRawChatMessages(raw json.RawMessage, fallback []openAIRequestMessage) []map[string]any {
	var messages []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &messages) != nil {
		input := make([]map[string]any, 0, len(fallback))
		for _, msg := range fallback {
			role := strings.TrimSpace(msg.Role)
			if role == "" {
				role = "user"
			}
			input = append(input, map[string]any{"role": role, "content": msg.Content.String()})
		}
		return input
	}
	input := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := rawString(msg["role"])
		if role == "" {
			role = "user"
		}
		switch role {
		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": rawString(msg["tool_call_id"]),
				"output":  chatContentText(msg["content"]),
			})
		case "assistant":
			if content, ok := responsesContentFromRawChatContent(role, msg["content"]); ok {
				input = append(input, map[string]any{"role": role, "content": content})
			}
			input = append(input, responsesFunctionCallsFromRawChatToolCalls(msg["tool_calls"])...)
		default:
			content, _ := responsesContentFromRawChatContent(role, msg["content"])
			input = append(input, map[string]any{"role": role, "content": content})
		}
	}
	return input
}

func responsesContentFromRawChatContent(role string, raw json.RawMessage) (any, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, strings.TrimSpace(text) != ""
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		textValue := chatContentText(raw)
		return textValue, strings.TrimSpace(textValue) != ""
	}
	converted := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		typeValue := rawString(part["type"])
		switch typeValue {
		case "text", "input_text":
			if value := rawString(part["text"]); value != "" {
				partType := "input_text"
				if role == "assistant" {
					partType = "output_text"
				}
				converted = append(converted, map[string]any{"type": partType, "text": value})
			}
		case "image_url":
			if imageURL := imageURLFromRawChatPart(part["image_url"]); imageURL != "" {
				converted = append(converted, map[string]any{"type": "input_image", "image_url": imageURL})
			}
		case "input_image":
			if imageURL := rawString(part["image_url"]); imageURL != "" {
				converted = append(converted, map[string]any{"type": "input_image", "image_url": imageURL})
			}
		}
	}
	if len(converted) == 0 {
		return "", false
	}
	return converted, true
}

func responsesFunctionCallsFromRawChatToolCalls(raw json.RawMessage) []map[string]any {
	var calls []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &calls) != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		var function map[string]json.RawMessage
		if err := json.Unmarshal(call["function"], &function); err != nil {
			continue
		}
		name := rawString(function["name"])
		if name == "" {
			continue
		}
		out = append(out, map[string]any{
			"type":      "function_call",
			"call_id":   rawString(call["id"]),
			"name":      name,
			"arguments": rawString(function["arguments"]),
		})
	}
	return out
}

func responsesToolsFromRawChatTools(raw json.RawMessage) []map[string]any {
	var tools []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &tools) != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if typeValue := rawString(tool["type"]); typeValue != "" && typeValue != "function" {
			continue
		}
		var function map[string]json.RawMessage
		if err := json.Unmarshal(tool["function"], &function); err != nil {
			continue
		}
		name := rawString(function["name"])
		if name == "" {
			continue
		}
		converted := map[string]any{"type": "function", "name": name}
		if description := rawString(function["description"]); description != "" {
			converted["description"] = description
		}
		if parameters, ok := rawJSONValue(function["parameters"]); ok {
			converted["parameters"] = parameters
		}
		if strict, ok := rawJSONValue(function["strict"]); ok {
			converted["strict"] = strict
		} else if strict, ok := rawJSONValue(tool["strict"]); ok {
			converted["strict"] = strict
		}
		out = append(out, converted)
	}
	return out
}

func responsesToolChoiceFromRaw(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, true
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return rawJSONValue(raw)
	}
	if rawString(obj["type"]) == "function" {
		var function map[string]json.RawMessage
		if err := json.Unmarshal(obj["function"], &function); err == nil {
			if name := rawString(function["name"]); name != "" {
				return map[string]any{"type": "function", "name": name}, true
			}
		}
	}
	return rawJSONValue(raw)
}

func imageURLFromRawChatPart(raw json.RawMessage) string {
	var image map[string]json.RawMessage
	if err := json.Unmarshal(raw, &image); err != nil {
		return rawString(raw)
	}
	return rawString(image["url"])
}

func chatContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := rawString(part["text"]); value != "" {
			texts = append(texts, value)
		}
	}
	return strings.Join(texts, "\n")
}

func copyRawJSONValue(dst map[string]any, src map[string]json.RawMessage, key string) {
	copyRawJSONValueAs(dst, src, key, key)
}

func copyRawJSONValueAs(dst map[string]any, src map[string]json.RawMessage, srcKey string, dstKey string) {
	if value, ok := rawJSONValue(src[srcKey]); ok {
		dst[dstKey] = value
	}
}

func rawJSONValue(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

func responsesStreamAsChatCompletionStream(ctx context.Context, responses io.ReadCloser, alias string) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer responses.Close()
		scanner := bufio.NewScanner(responses)
		scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024)
		toolCalls := map[string]*streamedToolCall{}
		sawToolCall := false
		wroteToolFinish := false
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				_ = writer.CloseWithError(ctx.Err())
				return
			default:
			}
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			if payload == "[DONE]" {
				if sawToolCall && !wroteToolFinish {
					if err := writeChatCompletionToolCallFinish(writer, alias); err != nil {
						_ = writer.CloseWithError(err)
						return
					}
				}
				_, _ = io.WriteString(writer, "data: [DONE]\n\n")
				_ = writer.Close()
				return
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal([]byte(payload), &obj); err != nil {
				continue
			}
			eventType := rawString(obj["type"])
			switch eventType {
			case "response.output_text.delta":
				delta := rawString(obj["delta"])
				if delta == "" {
					continue
				}
				if err := writeChatCompletionDelta(writer, alias, delta); err != nil {
					_ = writer.CloseWithError(err)
					return
				}
			case "response.output_item.added":
				call, ok := streamedToolCallFromResponseItem(obj["item"], rawInt(obj["output_index"], len(toolCalls)))
				if !ok {
					continue
				}
				if existing := streamedToolCallByCallID(toolCalls, call.CallID); existing != nil {
					toolCalls[call.ItemID] = existing
					if err := writeMissingToolArguments(writer, alias, existing, call.Arguments); err != nil {
						_ = writer.CloseWithError(err)
						return
					}
					continue
				}
				toolCalls[call.ItemID] = &call
				sawToolCall = true
				if err := writeChatCompletionToolCallStart(writer, alias, call); err != nil {
					_ = writer.CloseWithError(err)
					return
				}
				if call.Arguments != "" {
					if err := writeChatCompletionToolCallArguments(writer, alias, call.Index, call.Arguments); err != nil {
						_ = writer.CloseWithError(err)
						return
					}
				}
			case "response.function_call_arguments.delta":
				itemID := rawString(obj["item_id"])
				call := toolCalls[itemID]
				if call == nil {
					continue
				}
				delta := rawString(obj["delta"])
				if delta == "" {
					continue
				}
				call.Arguments += delta
				if err := writeChatCompletionToolCallArguments(writer, alias, call.Index, delta); err != nil {
					_ = writer.CloseWithError(err)
					return
				}
			case "response.function_call_arguments.done":
				itemID := rawString(obj["item_id"])
				call := toolCalls[itemID]
				if call == nil {
					continue
				}
				if err := writeMissingToolArguments(writer, alias, call, rawString(obj["arguments"])); err != nil {
					_ = writer.CloseWithError(err)
					return
				}
			case "response.output_item.done":
				call, ok := streamedToolCallFromResponseItem(obj["item"], 0)
				if !ok {
					continue
				}
				current := toolCalls[call.ItemID]
				if current == nil {
					current = streamedToolCallByCallID(toolCalls, call.CallID)
				}
				if current == nil {
					toolCalls[call.ItemID] = &call
					current = &call
					sawToolCall = true
					if err := writeChatCompletionToolCallStart(writer, alias, call); err != nil {
						_ = writer.CloseWithError(err)
						return
					}
				} else {
					toolCalls[call.ItemID] = current
				}
				if err := writeMissingToolArguments(writer, alias, current, call.Arguments); err != nil {
					_ = writer.CloseWithError(err)
					return
				}
			case "response.completed", "response.incomplete":
				if sawToolCall && !wroteToolFinish {
					if err := writeChatCompletionToolCallFinish(writer, alias); err != nil {
						_ = writer.CloseWithError(err)
						return
					}
					wroteToolFinish = true
				}
				usage := usageFromRawObject(obj)
				if usage != nil {
					if err := writeChatCompletionUsage(writer, alias, usage); err != nil {
						_ = writer.CloseWithError(err)
						return
					}
				}
				_, _ = io.WriteString(writer, "data: [DONE]\n\n")
				_ = writer.Close()
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		_ = writer.Close()
	}()
	return reader
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

type streamedToolCall struct {
	ItemID    string
	Index     int
	CallID    string
	Name      string
	Arguments string
}

func streamedToolCallFromResponseItem(raw json.RawMessage, fallbackIndex int) (streamedToolCall, bool) {
	var item map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &item) != nil {
		return streamedToolCall{}, false
	}
	if rawString(item["type"]) != "function_call" {
		return streamedToolCall{}, false
	}
	callID := rawString(item["call_id"])
	name := rawString(item["name"])
	if callID == "" || name == "" {
		return streamedToolCall{}, false
	}
	itemID := rawString(item["id"])
	if itemID == "" {
		itemID = callID
	}
	return streamedToolCall{
		ItemID:    itemID,
		Index:     fallbackIndex,
		CallID:    callID,
		Name:      name,
		Arguments: rawString(item["arguments"]),
	}, true
}

func rawInt(raw json.RawMessage, fallback int) int {
	if len(raw) == 0 {
		return fallback
	}
	var value int
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	var asFloat float64
	if err := json.Unmarshal(raw, &asFloat); err == nil {
		return int(asFloat)
	}
	return fallback
}

func streamedToolCallByCallID(calls map[string]*streamedToolCall, callID string) *streamedToolCall {
	if callID == "" {
		return nil
	}
	for _, call := range calls {
		if call != nil && call.CallID == callID {
			return call
		}
	}
	return nil
}

func writeMissingToolArguments(w io.Writer, alias string, call *streamedToolCall, fullArguments string) error {
	if call == nil || fullArguments == "" {
		return nil
	}
	missing := ""
	if strings.HasPrefix(fullArguments, call.Arguments) {
		missing = strings.TrimPrefix(fullArguments, call.Arguments)
	} else if fullArguments != call.Arguments {
		missing = fullArguments
	}
	call.Arguments = fullArguments
	if missing == "" {
		return nil
	}
	return writeChatCompletionToolCallArguments(w, alias, call.Index, missing)
}

func writeChatCompletionDelta(w io.Writer, alias string, delta string) error {
	chunk := map[string]any{
		"id":     "chatcmpl_ai_orch_responses_bridge",
		"object": "chat.completion.chunk",
		"model":  alias,
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{"content": delta},
			},
		},
	}
	return writeChatCompletionSSE(w, chunk)
}

func writeChatCompletionToolCallStart(w io.Writer, alias string, call streamedToolCall) error {
	chunk := map[string]any{
		"id":     "chatcmpl_ai_orch_responses_bridge",
		"object": "chat.completion.chunk",
		"model":  alias,
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []map[string]any{
						{
							"index": call.Index,
							"id":    call.CallID,
							"type":  "function",
							"function": map[string]any{
								"name":      call.Name,
								"arguments": "",
							},
						},
					},
				},
			},
		},
	}
	return writeChatCompletionSSE(w, chunk)
}

func writeChatCompletionToolCallArguments(w io.Writer, alias string, index int, delta string) error {
	chunk := map[string]any{
		"id":     "chatcmpl_ai_orch_responses_bridge",
		"object": "chat.completion.chunk",
		"model":  alias,
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{
					"tool_calls": []map[string]any{
						{
							"index": index,
							"function": map[string]any{
								"arguments": delta,
							},
						},
					},
				},
			},
		},
	}
	return writeChatCompletionSSE(w, chunk)
}

func writeChatCompletionToolCallFinish(w io.Writer, alias string) error {
	chunk := map[string]any{
		"id":     "chatcmpl_ai_orch_responses_bridge",
		"object": "chat.completion.chunk",
		"model":  alias,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "tool_calls",
			},
		},
	}
	return writeChatCompletionSSE(w, chunk)
}

func writeChatCompletionUsage(w io.Writer, alias string, usage map[string]any) error {
	chunkUsage := normalizeChatCompletionUsage(usage)
	chunk := map[string]any{
		"id":      "chatcmpl_ai_orch_responses_bridge",
		"object":  "chat.completion.chunk",
		"model":   alias,
		"choices": []any{},
		"usage":   chunkUsage,
	}
	return writeChatCompletionSSE(w, chunk)
}

func writeChatCompletionSSE(w io.Writer, chunk map[string]any) error {
	encoded, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "data: "+string(encoded)+"\n\n")
	return err
}

func normalizeChatCompletionUsage(usage map[string]any) map[string]any {
	out := make(map[string]any, len(usage)+3)
	for key, value := range usage {
		out[key] = value
	}
	if _, ok := out["prompt_tokens"]; !ok {
		if value, ok := usage["input_tokens"]; ok {
			out["prompt_tokens"] = value
		}
	}
	if _, ok := out["completion_tokens"]; !ok {
		if value, ok := usage["output_tokens"]; ok {
			out["completion_tokens"] = value
		}
	}
	if _, ok := out["total_tokens"]; !ok {
		if input, inputOK := numericValue(out["prompt_tokens"]); inputOK {
			if output, outputOK := numericValue(out["completion_tokens"]); outputOK {
				out["total_tokens"] = input + output
			}
		}
	}
	return out
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
	if responseRaw, ok := obj["response"]; ok {
		var response map[string]json.RawMessage
		if err := json.Unmarshal(responseRaw, &response); err == nil {
			if nested := usageFromRawObject(response); nested != nil {
				if usage == nil {
					usage = make(map[string]any, len(nested))
				}
				for key, value := range nested {
					usage[key] = value
				}
			}
		}
	}
	return usage
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
	copyUsageNumber(usage, parsed, "copilot_nano_aiu")
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
