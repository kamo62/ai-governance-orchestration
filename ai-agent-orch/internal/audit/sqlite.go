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
	// WAL mode supports multiple readers and one writer concurrently.
	// MaxOpenConns > 1 allows concurrent reads. Cap at 8 to avoid
	// excessive connection churn while still permitting parallelism.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)

	// WAL mode for better concurrent read/write performance.
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 10000;
		PRAGMA synchronous = NORMAL;
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite audit db: %w", err)
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
			parent_event_id TEXT,
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
	`)
	if err != nil {
		return err
	}
	if err := s.ensureAuditColumn("parent_event_id", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureAuditColumn("prev_event_hash", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureAuditColumn("event_hash", "TEXT"); err != nil {
		return err
	}
	_, err = s.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_events(session_id);
		CREATE INDEX IF NOT EXISTS idx_audit_type ON audit_events(event_type);
		CREATE INDEX IF NOT EXISTS idx_audit_recorded ON audit_events(recorded_at);
		CREATE INDEX IF NOT EXISTS idx_audit_parent ON audit_events(parent_event_id);
	`)
	return err
}

func (s *SQLiteStore) ensureAuditColumn(column string, definition string) error {
	rows, err := s.db.Query(`PRAGMA table_info(audit_events)`)
	if err != nil {
		return fmt.Errorf("inspect audit schema: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan audit schema: %w", err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate audit schema: %w", err)
	}

	if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE audit_events ADD COLUMN %s %s`, column, definition)); err != nil {
		return fmt.Errorf("add audit column %s: %w", column, err)
	}
	return nil
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
				event_id, parent_event_id, session_id, event_type, actor, agent, classification,
				reason, findings_json, prompt_sha256, estimated_cost_usd, cost_cap_usd,
				raw_prompt_stored, raw_response_stored, correlation_subject, recorded_at,
				prev_event_hash, event_hash, payload_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
		event.EventID, event.ParentEventID, event.SessionID, event.EventType, event.Actor, event.Agent,
		event.Classification, event.Reason, string(findingsJSON), event.PromptSHA256,
		event.EstimatedCostUSD, event.CostCapUSD,
		boolToInt(event.RawPromptStored), boolToInt(event.RawResponseStored),
		event.CorrelationSubject, event.RecordedAt.Format(time.RFC3339Nano),
		event.PrevEventHash, event.EventHash, string(payloadJSON),
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

// PurgeBefore removes audit events recorded before the given cutoff time.
func (s *SQLiteStore) PurgeBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("sqlite audit store is required")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_events WHERE recorded_at < ?`, cutoff.Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("purge audit events: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

// RetentionPolicy applies a retention window by purging events older than the given duration.
func (s *SQLiteStore) RetentionPolicy(ctx context.Context, maxAge time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	return s.PurgeBefore(ctx, cutoff)
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
