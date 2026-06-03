package governance

import "ai-agent-orch/internal/policyengine"

// ToolLoopCounter is an alias for the consolidated policy engine type.
type ToolLoopCounter = policyengine.ToolLoopCounter

// NewToolLoopCounter creates a counter with the given limit.
func NewToolLoopCounter(limit int) *ToolLoopCounter {
	return policyengine.NewToolLoopCounter(limit)
}
