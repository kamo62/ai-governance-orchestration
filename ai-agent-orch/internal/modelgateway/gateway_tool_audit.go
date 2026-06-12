package modelgateway

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

type toolCallAuditSummary struct {
	Count           int
	Names           []string
	TaskDelegations []observedTaskDelegation
}

type observedTaskDelegation struct {
	ToolCallID  string
	Agent       string
	Description string
	Prompt      string
}

type observedToolCall struct {
	Name      string
	Arguments string
}

type toolCallAuditTracker struct {
	keys    map[string]struct{}
	names   map[string]struct{}
	aliases map[string]string
	calls   map[string]*observedToolCall
}

func newToolCallAuditTracker() *toolCallAuditTracker {
	return &toolCallAuditTracker{
		keys:    make(map[string]struct{}),
		names:   make(map[string]struct{}),
		aliases: make(map[string]string),
		calls:   make(map[string]*observedToolCall),
	}
}

func (t *toolCallAuditTracker) ObserveChatCompletionSSELine(line string) {
	payload := sseDataPayload(line)
	if payload == "" || payload == "[DONE]" {
		return
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return
	}
	t.observeChatChoices(obj["choices"])
}

func (t *toolCallAuditTracker) ObserveResponsesSSELine(line string) {
	payload := sseDataPayload(line)
	if payload == "" || payload == "[DONE]" {
		return
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return
	}
	eventType := rawString(obj["type"])
	switch eventType {
	case "response.output_item.added", "response.output_item.done":
		t.observeResponsesItem(obj["item"], "stream")
	case "response.function_call_arguments.delta":
		t.observeResponsesArgumentsDelta(obj)
	case "response.function_call_arguments.done":
		t.observeResponsesArgumentsDone(obj)
	case "response.completed", "response.incomplete":
		var response map[string]json.RawMessage
		if raw := obj["response"]; len(raw) > 0 && json.Unmarshal(raw, &response) == nil {
			t.observeResponsesOutput(response["output"])
		}
	}
}

func (t *toolCallAuditTracker) Summary() toolCallAuditSummary {
	if t == nil || len(t.keys) == 0 {
		return toolCallAuditSummary{}
	}
	names := make([]string, 0, len(t.names))
	for name := range t.names {
		names = append(names, name)
	}
	sort.Strings(names)
	delegations := make([]observedTaskDelegation, 0)
	for key, call := range t.calls {
		if call == nil || !strings.EqualFold(sanitizeToolCallName(call.Name), "task") {
			continue
		}
		delegation, ok := parseTaskDelegationArguments(call.Arguments)
		if !ok {
			continue
		}
		delegation.ToolCallID = key
		delegations = append(delegations, delegation)
	}
	sort.Slice(delegations, func(i, j int) bool {
		if delegations[i].ToolCallID == delegations[j].ToolCallID {
			return delegations[i].Agent < delegations[j].Agent
		}
		return delegations[i].ToolCallID < delegations[j].ToolCallID
	})
	return toolCallAuditSummary{Count: len(t.keys), Names: names, TaskDelegations: delegations}
}

func (t *toolCallAuditTracker) observeChatChoices(raw json.RawMessage) {
	if t == nil || len(raw) == 0 {
		return
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &choices); err != nil {
		return
	}
	for choicePos, choice := range choices {
		choiceIndex := rawInt(choice["index"], choicePos)
		if len(choice["delta"]) > 0 {
			var delta map[string]json.RawMessage
			if err := json.Unmarshal(choice["delta"], &delta); err == nil {
				t.observeChatToolCalls(delta["tool_calls"], "choice:"+strconv.Itoa(choiceIndex))
			}
		}
		if len(choice["message"]) > 0 {
			var message map[string]json.RawMessage
			if err := json.Unmarshal(choice["message"], &message); err == nil {
				t.observeChatToolCalls(message["tool_calls"], "choice:"+strconv.Itoa(choiceIndex))
			}
		}
	}
}

func (t *toolCallAuditTracker) observeChatToolCalls(raw json.RawMessage, scope string) {
	if t == nil || len(raw) == 0 {
		return
	}
	var calls []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &calls); err != nil {
		return
	}
	for pos, call := range calls {
		name, arguments := chatToolCallFunction(call)
		id := rawString(call["id"])
		indexKey := ""
		if len(call["index"]) > 0 {
			indexKey = scope + ":index:" + strconv.Itoa(rawInt(call["index"], pos))
		}
		key := ""
		if id != "" {
			key = "id:" + id
			if indexKey != "" {
				t.moveAlias(indexKey, key)
			}
		} else if indexKey != "" {
			key = indexKey
			if existing := t.aliases[indexKey]; existing != "" {
				key = existing
			}
		} else if name != "" {
			key = scope + ":name:" + name + ":" + strconv.Itoa(pos)
		}
		t.remember(key, name, arguments, true)
	}
}

func (t *toolCallAuditTracker) observeResponsesOutput(raw json.RawMessage) {
	if t == nil || len(raw) == 0 {
		return
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return
	}
	for pos, item := range items {
		t.observeResponsesItem(item, "output:"+strconv.Itoa(pos))
	}
}

func (t *toolCallAuditTracker) observeResponsesItem(raw json.RawMessage, scope string) {
	if t == nil || len(raw) == 0 {
		return
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return
	}
	if rawString(item["type"]) != "function_call" {
		return
	}
	name := sanitizeToolCallName(rawString(item["name"]))
	key := rawString(item["call_id"])
	if key != "" {
		key = "call:" + key
	}
	itemKey := ""
	if id := rawString(item["id"]); id != "" {
		itemKey = "item:" + id
	}
	if key != "" && itemKey != "" {
		t.moveAlias(itemKey, key)
	} else if key == "" {
		key = itemKey
	}
	if key == "" && name != "" {
		key = scope + ":name:" + name
	}
	t.remember(key, name, rawString(item["arguments"]), false)
}

func (t *toolCallAuditTracker) observeResponsesArgumentsDelta(obj map[string]json.RawMessage) {
	if t == nil || obj == nil {
		return
	}
	key := responsesArgumentKey(obj)
	if key == "" {
		return
	}
	t.remember(key, "", rawString(obj["delta"]), true)
}

func (t *toolCallAuditTracker) observeResponsesArgumentsDone(obj map[string]json.RawMessage) {
	if t == nil || obj == nil {
		return
	}
	key := responsesArgumentKey(obj)
	if key == "" {
		return
	}
	t.remember(key, "", rawString(obj["arguments"]), false)
}

func responsesArgumentKey(obj map[string]json.RawMessage) string {
	if obj == nil {
		return ""
	}
	if callID := rawString(obj["call_id"]); callID != "" {
		return "call:" + callID
	}
	if itemID := rawString(obj["item_id"]); itemID != "" {
		return "item:" + itemID
	}
	if len(obj["output_index"]) > 0 {
		return "output:index:" + strconv.Itoa(rawInt(obj["output_index"], 0))
	}
	return ""
}

func (t *toolCallAuditTracker) moveAlias(from string, to string) {
	if t == nil || strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" || from == to {
		return
	}
	t.aliases[from] = to
	if call, ok := t.calls[from]; ok {
		delete(t.calls, from)
		t.calls[to] = call
	}
	if _, ok := t.keys[from]; ok {
		delete(t.keys, from)
		t.keys[to] = struct{}{}
	}
}

func (t *toolCallAuditTracker) remember(key string, name string, arguments string, appendArguments bool) {
	if t == nil || strings.TrimSpace(key) == "" {
		return
	}
	if existing := t.aliases[key]; existing != "" {
		key = existing
	}
	if _, ok := t.keys[key]; !ok {
		t.keys[key] = struct{}{}
	}
	call := t.calls[key]
	if call == nil {
		call = &observedToolCall{}
		t.calls[key] = call
	}
	if name = sanitizeToolCallName(name); name != "" {
		call.Name = name
		t.names[name] = struct{}{}
	}
	if arguments != "" {
		if appendArguments {
			call.Arguments += arguments
		} else {
			call.Arguments = arguments
		}
	}
}

func summarizeResponseToolCalls(body []byte) toolCallAuditSummary {
	if len(body) == 0 {
		return toolCallAuditSummary{}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return toolCallAuditSummary{}
	}
	tracker := newToolCallAuditTracker()
	tracker.observeChatChoices(obj["choices"])
	tracker.observeResponsesOutput(obj["output"])
	return tracker.Summary()
}

func chatToolCallName(call map[string]json.RawMessage) string {
	name, _ := chatToolCallFunction(call)
	return name
}

func chatToolCallFunction(call map[string]json.RawMessage) (string, string) {
	if call == nil {
		return "", ""
	}
	if len(call["function"]) > 0 {
		var fn map[string]json.RawMessage
		if err := json.Unmarshal(call["function"], &fn); err == nil {
			return sanitizeToolCallName(rawString(fn["name"])), rawString(fn["arguments"])
		}
	}
	return sanitizeToolCallName(rawString(call["name"])), rawString(call["arguments"])
}

func parseTaskDelegationArguments(arguments string) (observedTaskDelegation, bool) {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return observedTaskDelegation{}, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &obj); err != nil {
		return observedTaskDelegation{}, false
	}
	agent := sanitizeDelegatedAgentName(firstJSONString(obj, "subagent_type", "agent", "agent_name", "agentName", "subagent", "specialist", "name"))
	if agent == "" {
		return observedTaskDelegation{}, false
	}
	return observedTaskDelegation{
		Agent:       agent,
		Description: strings.TrimSpace(firstJSONString(obj, "description", "title", "task", "reason")),
		Prompt:      strings.TrimSpace(firstJSONString(obj, "prompt", "instructions", "input")),
	}, true
}

func firstJSONString(obj map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if raw := obj[key]; len(raw) > 0 {
			if value := rawString(raw); value != "" {
				return value
			}
		}
	}
	return ""
}

func sanitizeDelegatedAgentName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return ""
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == ':' || r == '/':
		default:
			return ""
		}
	}
	return name
}

func sseDataPayload(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:"))
}

func sanitizeToolCallName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if len(name) > 80 {
		name = name[:80]
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == ':' || r == '/':
		default:
			return ""
		}
	}
	return name
}
