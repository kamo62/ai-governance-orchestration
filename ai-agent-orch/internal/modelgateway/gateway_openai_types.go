package modelgateway

import (
	"strings"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/openrouter"
)

func inferTaskType(messages []openAIRequestMessage) string {
	if len(messages) == 0 {
		return "general"
	}
	return inferTaskTypeFromText(messages[len(messages)-1].Content.String())
}

func inferTaskTypeFromInput(input []openAIResponsesInput) string {
	if len(input) == 0 {
		return "general"
	}
	return inferTaskTypeFromText(input[len(input)-1].Content.String())
}

func inferTaskTypeFromText(text string) string {
	content := strings.ToLower(text)
	switch {
	case strings.Contains(content, "test") || strings.Contains(content, "spec"):
		return "test"
	case strings.Contains(content, "review") || strings.Contains(content, "audit"):
		return "review"
	case strings.Contains(content, "refactor") || strings.Contains(content, "architecture"):
		return "architecture"
	case strings.Contains(content, "implement") || strings.Contains(content, "code"):
		return "coding"
	default:
		return "general"
	}
}

func convertMessages(msgs []openAIRequestMessage) []openrouter.Message {
	out := make([]openrouter.Message, len(msgs))
	for i, m := range msgs {
		out[i] = openrouter.Message{Role: m.Role, Content: m.Content.String()}
	}
	return out
}

func convertChoices(choices []struct {
	Message openrouter.Message `json:"message"`
}) []openAIChoice {
	out := make([]openAIChoice, len(choices))
	for i, c := range choices {
		out[i] = openAIChoice{
			Index:   i,
			Message: openAIMessage{Role: c.Message.Role, Content: c.Message.Content},
		}
	}
	return out
}

func convertResponsesInput(input []openAIResponsesInput) []openrouter.Message {
	out := make([]openrouter.Message, 0, len(input))
	for _, item := range input {
		role := item.Role
		if role == "" {
			role = "user"
		}
		out = append(out, openrouter.Message{Role: role, Content: item.Content.String()})
	}
	return out
}

type openAIChatCompletionRequest struct {
	Model       string                 `json:"model"`
	Messages    []openAIRequestMessage `json:"messages"`
	Temperature float64                `json:"temperature,omitempty"`
	MaxTokens   int                    `json:"max_tokens,omitempty"`
	Stream      bool                   `json:"stream,omitempty"`
}

type openAIRequestMessage struct {
	Role    string         `json:"role"`
	Content rawTextContent `json:"content"`
}

type rawTextContent string

type openAIResponsesRequest struct {
	Model  string                  `json:"model"`
	Input  openAIResponsesInputSet `json:"input"`
	Stream bool                    `json:"stream,omitempty"`
}

type openAIResponsesInput struct {
	Role    string         `json:"role"`
	Type    string         `json:"type"`
	Content rawTextContent `json:"content"`
	Output  string         `json:"output"`
	Name    string         `json:"name"`
	Args    string         `json:"arguments"`
}
