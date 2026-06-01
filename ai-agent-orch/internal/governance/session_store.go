package governance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SessionRecord is the durable representation of a user session.
type SessionRecord struct {
	SessionID      string
	ActorSubject   string
	Agent          string
	Classification string
	PromptSHA256   string
	Status         string // created, awaiting_confirmation, confirming, confirmed, running, done, failed, confirm_failed, aborted
	CreatedAt      time.Time
}

// SessionStore persists and queries session records with ownership enforcement.
type SessionStore interface {
	Create(ctx context.Context, rec SessionRecord) error
	Get(ctx context.Context, sessionID string) (SessionRecord, error)
	UpdateStatus(ctx context.Context, sessionID, status string) error
	CompareAndSwapStatus(ctx context.Context, sessionID, from, to string) error
}

// SQLiteSessionStore implements SessionStore backed by SQLite.
type SQLiteSessionStore struct {
	db *sql.DB
}

func NewSQLiteSessionStore(dbPath string) (*SQLiteSessionStore, error) {
	if dbPath == "" {
		return nil, errors.New("sqlite session db path is required")
	}
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, fmt.Errorf("create session db directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite session db: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 10000;
		PRAGMA synchronous = NORMAL;
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite session db: %w", err)
	}
	store := &SQLiteSessionStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sessions: %w", err)
	}
	if dbPath != ":memory:" {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set sqlite session db permissions: %w", err)
		}
	}
	return store, nil
}

func (s *SQLiteSessionStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			actor_subject TEXT NOT NULL,
			agent TEXT NOT NULL,
			classification TEXT NOT NULL,
			prompt_sha256 TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_sessions_actor ON sessions(actor_subject);
		CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status);
	`)
	return err
}

func (s *SQLiteSessionStore) Create(ctx context.Context, rec SessionRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (session_id, actor_subject, agent, classification, prompt_sha256, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, rec.SessionID, rec.ActorSubject, rec.Agent, rec.Classification, rec.PromptSHA256, rec.Status, rec.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *SQLiteSessionStore) Get(ctx context.Context, sessionID string) (SessionRecord, error) {
	var rec SessionRecord
	var createdAtStr string
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, actor_subject, agent, classification, prompt_sha256, status, created_at
		FROM sessions WHERE session_id = ?
	`, sessionID).Scan(&rec.SessionID, &rec.ActorSubject, &rec.Agent, &rec.Classification, &rec.PromptSHA256, &rec.Status, &createdAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, fmt.Errorf("session not found")
	}
	if err != nil {
		return SessionRecord{}, fmt.Errorf("query session: %w", err)
	}
	rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	return rec, nil
}

func (s *SQLiteSessionStore) UpdateStatus(ctx context.Context, sessionID, status string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET status = ? WHERE session_id = ?`, status, sessionID)
	if err != nil {
		return fmt.Errorf("update session status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

func (s *SQLiteSessionStore) CompareAndSwapStatus(ctx context.Context, sessionID, from, to string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var current string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM sessions WHERE session_id = ?`, sessionID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("session not found")
		}
		return fmt.Errorf("select status: %w", err)
	}
	if current != from {
		return fmt.Errorf("invalid state transition: expected %q, got %q", from, current)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET status = ? WHERE session_id = ?`, to, sessionID); err != nil {
		return fmt.Errorf("update status: %w", err)
	}
	return tx.Commit()
}

// Close closes the underlying database connection.
func (s *SQLiteSessionStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
