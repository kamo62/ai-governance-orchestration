package governance

import "testing"

func TestToolLoopCounterBlocksAtConfiguredLimitAndResetsOnOutput(t *testing.T) {
	counter := NewToolLoopCounter(2)

	if blocked := counter.Observe("mcp_call"); blocked {
		t.Fatal("first tool call should not block")
	}
	if blocked := counter.Observe("stream"); blocked {
		t.Fatal("agent output should reset without blocking")
	}
	if blocked := counter.Observe("mcp_call"); blocked {
		t.Fatal("first tool call after reset should not block")
	}
	if blocked := counter.Observe("mcp_call"); blocked {
		t.Fatal("second consecutive tool call should not block")
	}
	if blocked := counter.Observe("mcp_call"); !blocked {
		t.Fatal("third consecutive tool call should block when limit is 2")
	}
}
