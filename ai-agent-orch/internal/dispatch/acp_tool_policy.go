package dispatch

import (
	"strings"
)

func selectedACPOptionID(params map[string]any, allowed bool) string {
	if options, ok := params["options"].([]any); ok {
		if allowed {
			for _, option := range options {
				item, ok := option.(map[string]any)
				if !ok {
					continue
				}
				kind, _ := item["kind"].(string)
				id, _ := item["optionId"].(string)
				if id != "" && kind == "allow_once" {
					return id
				}
			}
		}
		for _, option := range options {
			item, ok := option.(map[string]any)
			if !ok {
				continue
			}
			kind, _ := item["kind"].(string)
			id, _ := item["optionId"].(string)
			if id == "" {
				continue
			}
			if strings.HasPrefix(kind, "reject") || strings.HasPrefix(kind, "deny") {
				return id
			}
		}
	}
	if allowed {
		return "once"
	}
	return "reject"
}

func (h *acpHandle) acpToolAllowed(toolName string) bool {
	command, subcommand := acpToolPolicyCommand(toolName)
	if command == "write_file" {
		return h.acpWritesAllowed()
	}
	if !h.sessionConfigDeclaresACPTool(command, subcommand) {
		return false
	}
	if h.config.ToolBroker != nil && strings.TrimSpace(h.config.AgentName) != "" {
		return h.config.ToolBroker.ValidateWithPermissions(command, subcommand, h.config.AgentName, h.config.Permissions) == nil
	}
	return true
}

func (h *acpHandle) sessionConfigDeclaresACPTool(command, subcommand string) bool {
	for _, allowedTool := range h.config.AllowedTools {
		allowedCommand, allowedSubcommand := ParseToolCommand(allowedTool)
		if allowedCommand == command && (allowedSubcommand == "" || allowedSubcommand == subcommand) {
			return true
		}
	}
	return false
}

func (h *acpHandle) acpWritesAllowed() bool {
	if strings.EqualFold(strings.TrimSpace(h.config.Permissions["workspace_write"]), "deny") {
		return false
	}
	if !h.sessionConfigDeclaresACPTool("write_file", "") {
		return false
	}
	if h.config.ToolBroker != nil && strings.TrimSpace(h.config.AgentName) != "" {
		return h.config.ToolBroker.ValidateWithPermissions("write_file", "", h.config.AgentName, h.config.Permissions) == nil
	}
	return true
}

func acpToolCommand(toolName string) string {
	name := strings.ToLower(strings.TrimSpace(toolName))
	switch name {
	case "fs/write_text_file", "fs.write_text_file", "fs/writetextfile", "fs.writetextfile", "write_text_file", "writefile", "write_file":
		return "write_file"
	case "fs/read_text_file", "fs.read_text_file", "fs/readtextfile", "fs.readtextfile", "read_text_file", "readfile", "read_file":
		return "read_file"
	case "bash", "shell", "terminal", "run_command":
		return "run_command"
	default:
		return name
	}
}

func acpToolPolicyCommand(toolName string) (string, string) {
	normalized := acpToolCommand(toolName)
	command, subcommand := ParseToolCommand(normalized)
	return command, subcommand
}

// extractPatchFromResult attempts to extract a patch envelope JSON string from an ACP result.
func extractPatchFromResult(result map[string]any) string {
	if result == nil {
		return ""
	}
	if content, ok := result["content"].(string); ok && content != "" {
		return content
	}
	if payload, ok := result["patch"].(string); ok && payload != "" {
		return payload
	}
	if payload, ok := result["output"].(string); ok && payload != "" {
		return payload
	}
	return ""
}

// extractPatchFromText scans raw text for a JSON patch envelope.
func extractPatchFromText(text string) string {
	// Look for JSON object starting with {"patch" or {"file_path"
	// This is a heuristic for extracting inline patch JSON from agent text output.
	for i := 0; i < len(text); i++ {
		if text[i] == '{' {
			// Try to find matching closing brace
			depth := 1
			for j := i + 1; j < len(text) && depth > 0; j++ {
				if text[j] == '{' {
					depth++
				} else if text[j] == '}' {
					depth--
				}
				if depth == 0 {
					sub := text[i : j+1]
					// Heuristic: must contain patch-related keys
					if containsPatchKey(sub) {
						return sub
					}
					break
				}
			}
		}
	}
	return ""
}

func containsPatchKey(s string) bool {
	if len(s) <= 20 || !strings.Contains(s, "\"files\"") {
		return false
	}
	return strings.Contains(s, "\"patch\"") ||
		strings.Contains(s, "\"patchId\"") ||
		strings.Contains(s, "\"patch_id\"")
}
