package dispatch

import (
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
)

// ToolBroker validates runtime tool calls against the command allowlist.
type ToolBroker = policyengine.ToolBroker

// NewToolBroker creates a broker from the given policy path.
func NewToolBroker(path string) (*ToolBroker, error) {
	return policyengine.NewToolBroker(path)
}

// ParseToolCommand splits a tool identifier like "run_command:playwright"
// into command and subcommand.
func ParseToolCommand(tool string) (command, subcommand string) {
	return policyengine.ParseToolCommand(tool)
}
