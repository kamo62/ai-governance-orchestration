package governance

type ToolLoopCounter struct {
	limit       int
	consecutive int
}

func NewToolLoopCounter(limit int) *ToolLoopCounter {
	return &ToolLoopCounter{limit: limit}
}

func (c *ToolLoopCounter) Observe(eventType string) bool {
	if c == nil || c.limit <= 0 {
		return false
	}
	switch eventType {
	case "mcp_call", "tool_call", "command":
		c.consecutive++
		return c.consecutive > c.limit
	case "stream", "patch", "done":
		c.consecutive = 0
	}
	return false
}

func (c *ToolLoopCounter) Count() int {
	if c == nil {
		return 0
	}
	return c.consecutive
}
