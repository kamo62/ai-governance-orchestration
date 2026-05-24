package audit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStoreAppendAndRetrieve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	event := Event{
		EventID:         "evt_test_1",
		SessionID:       "sess_123",
		EventType:       "session.created",
		Actor:           "test",
		Agent:           "test-generation",
		Classification:  "internal",
		RawPromptStored: false,
		RecordedAt:      time.Now().UTC(),
	}

	ret, err := store.Append(ctx, event)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if ret.EventID != event.EventID {
		t.Fatalf("expected event ID %s, got %s", event.EventID, ret.EventID)
	}

	events, err := store.EventsBySession(ctx, "sess_123")
	if err != nil {
		t.Fatalf("events by session: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Agent != "test-generation" {
		t.Fatalf("expected agent test-generation, got %s", events[0].Agent)
	}
}

func TestSQLiteStoreMultipleSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = store.Append(ctx, Event{EventID: fmtID(i), SessionID: "sess_a", EventType: "test", RecordedAt: time.Now().UTC()})
	}
	for i := 3; i < 5; i++ {
		_, _ = store.Append(ctx, Event{EventID: fmtID(i), SessionID: "sess_b", EventType: "test", RecordedAt: time.Now().UTC()})
	}

	aEvents, err := store.EventsBySession(ctx, "sess_a")
	if err != nil {
		t.Fatalf("query sess_a: %v", err)
	}
	if len(aEvents) != 3 {
		t.Fatalf("expected 3 events for sess_a, got %d", len(aEvents))
	}

	bEvents, err := store.EventsBySession(ctx, "sess_b")
	if err != nil {
		t.Fatalf("query sess_b: %v", err)
	}
	if len(bEvents) != 2 {
		t.Fatalf("expected 2 events for sess_b, got %d", len(bEvents))
	}
}

func TestSQLiteStoreImportFromFileStore(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "audit.jsonl")
	dbPath := filepath.Join(dir, "audit.db")

	fs := NewFileStore(jsonlPath)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = fs.Append(ctx, Event{EventID: fmtID(i), SessionID: "sess_import", EventType: "test", RecordedAt: time.Now().UTC()})
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer store.Close()

	if err := store.ImportFromFileStore(ctx, fs); err != nil {
		t.Fatalf("import: %v", err)
	}

	events, err := store.EventsBySession(ctx, "sess_import")
	if err != nil {
		t.Fatalf("query after import: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 imported events, got %d", len(events))
	}
}

func TestSQLiteStoreEmptySession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer store.Close()

	events, err := store.EventsBySession(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("query empty: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestSQLiteStoreUsesPrivateDatabasePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer store.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat sqlite db: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected sqlite db permissions 0600, got %o", got)
	}
}

func fmtID(i int) string {
	return fmt.Sprintf("evt_%d", i)
}
