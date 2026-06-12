package policyengine

import "sync"

// ToolLoopCounter enforces a limit on consecutive tool/MCP calls.
type ToolLoopCounter struct {
	mu          sync.Mutex
	limit       int
	consecutive int
}

// NewToolLoopCounter creates a counter with the given limit.
// A limit <= 0 disables enforcement.
func NewToolLoopCounter(limit int) *ToolLoopCounter {
	return &ToolLoopCounter{limit: limit}
}

// Observe records an event and returns true if the consecutive tool call limit
// has been exceeded. Event types that represent agent output (stream, patch, done)
// reset the counter.
func (c *ToolLoopCounter) Observe(eventType string) bool {
	if c == nil || c.limit <= 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch eventType {
	case "mcp_call", "tool_call", "command":
		c.consecutive++
		return c.consecutive > c.limit
	case "stream", "patch", "done":
		c.consecutive = 0
	}
	return false
}
