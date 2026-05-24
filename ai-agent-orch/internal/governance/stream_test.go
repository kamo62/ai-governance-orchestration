package governance

import (
	"fmt"
	"testing"
)

func TestEventStoreBoundsClosedSessionHistory(t *testing.T) {
	store := NewEventStore()

	for i := 0; i < maxClosedSessionHistory+20; i++ {
		sessionID := fmt.Sprintf("sess_%03d", i)
		store.Publish(sessionID, SessionEvent{Type: "stream", Payload: "event"})
		store.Close(sessionID)
	}

	store.mu.RLock()
	defer store.mu.RUnlock()

	if len(store.history) > maxClosedSessionHistory {
		t.Fatalf("expected at most %d history entries, got %d", maxClosedSessionHistory, len(store.history))
	}
	if len(store.closed) > maxClosedSessionHistory {
		t.Fatalf("expected at most %d closed entries, got %d", maxClosedSessionHistory, len(store.closed))
	}
}
