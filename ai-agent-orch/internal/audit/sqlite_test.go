package audit

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
)

func TestSQLiteStoreLegacyMigrationDoesNotHoldSchemaRowsDuringDDL(t *testing.T) {
	dsn := "file:audit_legacy_migration?mode=memory&cache=shared"
	db, err := sqlitex.Open(dsn, "audit migration test")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE audit_events (event_id TEXT PRIMARY KEY, session_id TEXT, event_type TEXT NOT NULL, recorded_at TEXT NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- (&SQLiteStore{DBPath: dsn, Now: time.Now, db: db}).migrate() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("migration timed out; possible open PRAGMA rows during DDL")
	}
}

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
		Agent:           "unit-tests",
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
	if events[0].Agent != "unit-tests" {
		t.Fatalf("expected agent unit-tests, got %s", events[0].Agent)
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

func TestSQLiteStorePersistsHashColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	chain := NewChainAppender(store)
	e1, err := chain.Append(ctx, Event{EventID: "evt_hash_1", SessionID: "sess_hash", EventType: "session.created"})
	if err != nil {
		t.Fatalf("append first event: %v", err)
	}
	e2, err := chain.Append(ctx, Event{EventID: "evt_hash_2", SessionID: "sess_hash", EventType: "session.confirmed"})
	if err != nil {
		t.Fatalf("append second event: %v", err)
	}

	var prevHash, eventHash string
	if err := store.db.QueryRowContext(ctx, `
		SELECT prev_event_hash, event_hash FROM audit_events WHERE event_id = ?
	`, e2.EventID).Scan(&prevHash, &eventHash); err != nil {
		t.Fatalf("query hash columns: %v", err)
	}
	if prevHash != e1.EventHash {
		t.Fatalf("expected prev hash %s, got %s", e1.EventHash, prevHash)
	}
	if eventHash != e2.EventHash {
		t.Fatalf("expected event hash %s, got %s", e2.EventHash, eventHash)
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

func TestSQLiteStoreMigratesParentEventIDBeforeIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE audit_events (
			event_id TEXT PRIMARY KEY,
			session_id TEXT,
			event_type TEXT NOT NULL,
			actor TEXT,
			agent TEXT,
			classification TEXT,
			reason TEXT,
			findings_json TEXT,
			prompt_sha256 TEXT,
			estimated_cost_usd REAL,
			cost_cap_usd REAL,
			raw_prompt_stored INTEGER NOT NULL DEFAULT 0,
			raw_response_stored INTEGER NOT NULL DEFAULT 0,
			correlation_subject TEXT,
			recorded_at TEXT NOT NULL,
			payload_json TEXT
		);
	`); err != nil {
		_ = db.Close()
		t.Fatalf("create legacy audit schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("migrate legacy sqlite store: %v", err)
	}
	defer store.Close()

	if _, err := store.Append(context.Background(), Event{
		EventID:       "evt_child",
		ParentEventID: "evt_parent",
		SessionID:     "sess_legacy",
		EventType:     "session.confirmed",
	}); err != nil {
		t.Fatalf("append linked event after migration: %v", err)
	}
}

func fmtID(i int) string {
	return fmt.Sprintf("evt_%d", i)
}
