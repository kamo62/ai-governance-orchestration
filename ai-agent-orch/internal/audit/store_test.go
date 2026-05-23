package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreAppendsJSONLEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	store := NewFileStore(path)
	store.Now = func() time.Time { return time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC) }

	first, err := store.Append(context.Background(), Event{
		EventID:            "evt_1",
		SessionID:          "sess_1",
		EventType:          "session.created",
		Actor:              "local-dev",
		Agent:              "test-generation",
		Classification:     "internal",
		RawPromptStored:    false,
		RawResponseStored:  false,
		PromptSHA256:       "hash-1",
		CorrelationSubject: "governance-shell",
	})
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}
	_, err = store.Append(context.Background(), Event{
		EventID:            "evt_2",
		SessionID:          "sess_1",
		EventType:          "session.accepted",
		Actor:              "local-dev",
		RawPromptStored:    false,
		RawResponseStored:  false,
		CorrelationSubject: "orchestrator",
	})
	if err != nil {
		t.Fatalf("append second event: %v", err)
	}

	events := readEvents(t, path)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if first.RecordedAt.IsZero() {
		t.Fatalf("expected recorded timestamp")
	}
	if events[0].EventID != "evt_1" || events[1].EventID != "evt_2" {
		t.Fatalf("events were not appended in order: %#v", events)
	}
	if events[0].RawPromptStored || events[0].RawResponseStored {
		t.Fatalf("raw content flags must default to false: %#v", events[0])
	}
}

func TestFileStoreReturnsEventsForSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	store := NewFileStore(path)

	for _, event := range []Event{
		{EventID: "evt_1", SessionID: "sess_match", EventType: "session.created"},
		{EventID: "evt_2", SessionID: "sess_other", EventType: "session.created"},
		{EventID: "evt_3", SessionID: "sess_match", EventType: "orchestrator.session.accepted"},
		{EventID: "evt_4", EventType: "session.denied"},
	} {
		if _, err := store.Append(context.Background(), event); err != nil {
			t.Fatalf("append event %s: %v", event.EventID, err)
		}
	}

	events, err := store.EventsBySession(context.Background(), "sess_match")
	if err != nil {
		t.Fatalf("EventsBySession returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %#v", len(events), events)
	}
	if events[0].EventID != "evt_1" || events[1].EventID != "evt_3" {
		t.Fatalf("events were not returned in file order: %#v", events)
	}
}

func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("parse audit event: %v", err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan audit file: %v", err)
	}
	return events
}
