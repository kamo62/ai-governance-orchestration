package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore is a durable audit backend backed by SQLite.
// It satisfies the same interface as FileStore and can replace it
// in multi-user or query-heavy deployments.
type SQLiteStore struct {
	DBPath string
	Now    func() time.Time
	db     *sql.DB
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath == "" {
		return nil, errors.New("sqlite audit db path is required")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create audit db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite audit db: %w", err)
	}

	// WAL mode for better concurrent read/write performance.
	if _, err := db.Exec(`PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite wal mode: %w", err)
	}

	store := &SQLiteStore{
		DBPath: dbPath,
		Now:    func() time.Time { return time.Now().UTC() },
		db:     db,
	}

	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite audit db: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite audit db permissions: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_events (
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
		CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_events(session_id);
		CREATE INDEX IF NOT EXISTS idx_audit_type ON audit_events(event_type);
		CREATE INDEX IF NOT EXISTS idx_audit_recorded ON audit_events(recorded_at);
	`)
	return err
}

func (s *SQLiteStore) Append(ctx context.Context, event Event) (Event, error) {
	if s == nil || s.db == nil {
		return Event{}, errors.New("sqlite audit store is required")
	}
	if event.EventID == "" {
		return Event{}, errors.New("audit event_id is required")
	}
	if event.EventType == "" {
		return Event{}, errors.New("audit event_type is required")
	}
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	if event.RecordedAt.IsZero() {
		now := s.Now
		if now == nil {
			now = func() time.Time { return time.Now().UTC() }
		}
		event.RecordedAt = now().UTC()
	}

	findingsJSON, err := json.Marshal(event.Findings)
	if err != nil {
		return Event{}, fmt.Errorf("encode findings: %w", err)
	}

	// Store full event as JSON for schema flexibility.
	payloadJSON, err := json.Marshal(event)
	if err != nil {
		return Event{}, fmt.Errorf("encode payload: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO audit_events (
			event_id, session_id, event_type, actor, agent, classification,
			reason, findings_json, prompt_sha256, estimated_cost_usd, cost_cap_usd,
			raw_prompt_stored, raw_response_stored, correlation_subject, recorded_at, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		event.EventID, event.SessionID, event.EventType, event.Actor, event.Agent,
		event.Classification, event.Reason, string(findingsJSON), event.PromptSHA256,
		event.EstimatedCostUSD, event.CostCapUSD,
		boolToInt(event.RawPromptStored), boolToInt(event.RawResponseStored),
		event.CorrelationSubject, event.RecordedAt.Format(time.RFC3339Nano), string(payloadJSON),
	)
	if err != nil {
		return Event{}, fmt.Errorf("insert audit event: %w", err)
	}

	return event, nil
}

func (s *SQLiteStore) EventsBySession(ctx context.Context, sessionID string) ([]Event, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite audit store is required")
	}
	if sessionID == "" {
		return nil, errors.New("session_id is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT payload_json FROM audit_events
		WHERE session_id = ?
		ORDER BY recorded_at ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		var event Event
		if err := json.Unmarshal([]byte(payloadJSON), &event); err != nil {
			return nil, fmt.Errorf("parse audit event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}

// ImportFromFileStore migrates existing JSONL audit data into SQLite.
func (s *SQLiteStore) ImportFromFileStore(ctx context.Context, fs *FileStore) error {
	if fs == nil || fs.Path == "" {
		return nil
	}
	events, err := fs.AllEvents(ctx)
	if err != nil {
		return fmt.Errorf("read file store for migration: %w", err)
	}
	for _, event := range events {
		if _, err := s.Append(ctx, event); err != nil {
			return fmt.Errorf("migrate event %s: %w", event.EventID, err)
		}
	}
	return nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
