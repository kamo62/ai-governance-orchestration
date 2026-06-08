package governance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Governed runtime binding fields.
	RunID          string
	PermissionMode string
	ApprovalMode   string
	WorkspaceMode  string
	// Phase 1F control-plane binding fields.
	UseCaseID    string
	WorkflowID   string
	WorkItemID   string
	WorkItemType string
	RepoURL      string
	Branch       string
	CommitSHA    string
	Intent       string
	ActorHint    string
	SourceSystem string
	// Phase 1F cost/value sizing.
	StoryPoints         int
	EstimatedDevDays    float64
	BlendedDayRateUSD   float64
	BaselineCostUSD     float64
	ModelCostUSD        float64
	ToolCostUSD         float64
	PlatformCostUSD     float64
	ReviewCostUSD       float64
	VerificationCostUSD float64
	RetryCount          int
}

// SessionStore persists and queries session records with ownership enforcement.
type SessionStore interface {
	Create(ctx context.Context, rec SessionRecord) error
	Get(ctx context.Context, sessionID string) (SessionRecord, error)
	ListRecent(ctx context.Context, actorSubject string, limit int) ([]SessionRecord, error)
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
	if err != nil {
		return err
	}
	// Phase 1F migrations: add control-plane binding and sizing columns.
	cols := map[string]string{
		"run_id":                "TEXT NOT NULL DEFAULT ''",
		"permission_mode":       "TEXT NOT NULL DEFAULT 'reviewed'",
		"approval_mode":         "TEXT NOT NULL DEFAULT 'manual'",
		"workspace_mode":        "TEXT NOT NULL DEFAULT ''",
		"use_case_id":           "TEXT NOT NULL DEFAULT ''",
		"workflow_id":           "TEXT NOT NULL DEFAULT ''",
		"work_item_id":          "TEXT NOT NULL DEFAULT ''",
		"work_item_type":        "TEXT NOT NULL DEFAULT ''",
		"repo_url":              "TEXT NOT NULL DEFAULT ''",
		"branch":                "TEXT NOT NULL DEFAULT ''",
		"commit_sha":            "TEXT NOT NULL DEFAULT ''",
		"intent":                "TEXT NOT NULL DEFAULT ''",
		"actor_hint":            "TEXT NOT NULL DEFAULT ''",
		"source_system":         "TEXT NOT NULL DEFAULT ''",
		"story_points":          "INTEGER NOT NULL DEFAULT 0",
		"estimated_dev_days":    "REAL NOT NULL DEFAULT 0",
		"blended_day_rate_usd":  "REAL NOT NULL DEFAULT 0",
		"baseline_cost_usd":     "REAL NOT NULL DEFAULT 0",
		"model_cost_usd":        "REAL NOT NULL DEFAULT 0",
		"tool_cost_usd":         "REAL NOT NULL DEFAULT 0",
		"platform_cost_usd":     "REAL NOT NULL DEFAULT 0",
		"review_cost_usd":       "REAL NOT NULL DEFAULT 0",
		"verification_cost_usd": "REAL NOT NULL DEFAULT 0",
		"retry_count":           "INTEGER NOT NULL DEFAULT 0",
	}
	for col, def := range cols {
		if err := s.ensureSessionColumn(col, def); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteSessionStore) ensureSessionColumn(column string, definition string) error {
	rows, err := s.db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return fmt.Errorf("inspect sessions schema: %w", err)
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
			return fmt.Errorf("scan sessions schema: %w", err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sessions schema: %w", err)
	}
	if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE sessions ADD COLUMN %s %s`, column, definition)); err != nil {
		// Another process may win the PRAGMA table_info -> ALTER TABLE race.
		// Treat only SQLite's duplicate-column error as success.
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return nil
		}
		return fmt.Errorf("add sessions column %s: %w", column, err)
	}
	return nil
}

func (s *SQLiteSessionStore) Create(ctx context.Context, rec SessionRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			session_id, actor_subject, agent, classification, prompt_sha256, status, created_at,
			run_id, permission_mode, approval_mode, workspace_mode,
			use_case_id, workflow_id, work_item_id, work_item_type, repo_url, branch, commit_sha, intent, actor_hint, source_system,
			story_points, estimated_dev_days, blended_day_rate_usd, baseline_cost_usd,
			model_cost_usd, tool_cost_usd, platform_cost_usd, review_cost_usd,
			verification_cost_usd, retry_count
		) VALUES (?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?)
	`,
		rec.SessionID, rec.ActorSubject, rec.Agent, rec.Classification, rec.PromptSHA256, rec.Status, rec.CreatedAt.Format(time.RFC3339Nano),
		rec.RunID, rec.PermissionMode, rec.ApprovalMode, rec.WorkspaceMode,
		rec.UseCaseID, rec.WorkflowID, rec.WorkItemID, rec.WorkItemType, rec.RepoURL, rec.Branch, rec.CommitSHA, rec.Intent, rec.ActorHint, rec.SourceSystem,
		rec.StoryPoints, rec.EstimatedDevDays, rec.BlendedDayRateUSD, rec.BaselineCostUSD,
		rec.ModelCostUSD, rec.ToolCostUSD, rec.PlatformCostUSD, rec.ReviewCostUSD,
		rec.VerificationCostUSD, rec.RetryCount,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *SQLiteSessionStore) Get(ctx context.Context, sessionID string) (SessionRecord, error) {
	var rec SessionRecord
	var createdAtStr string
	err := s.db.QueryRowContext(ctx, `
			SELECT
				session_id, actor_subject, agent, classification, prompt_sha256, status, created_at,
				COALESCE(run_id, ''), COALESCE(permission_mode, 'reviewed'), COALESCE(approval_mode, 'manual'), COALESCE(workspace_mode, ''),
				COALESCE(use_case_id, ''), COALESCE(workflow_id, ''), COALESCE(work_item_id, ''), COALESCE(work_item_type, ''),
				COALESCE(repo_url, ''), COALESCE(branch, ''), COALESCE(commit_sha, ''), COALESCE(intent, ''), COALESCE(actor_hint, ''), COALESCE(source_system, ''),
				COALESCE(story_points, 0), COALESCE(estimated_dev_days, 0), COALESCE(blended_day_rate_usd, 0),
				COALESCE(baseline_cost_usd, 0), COALESCE(model_cost_usd, 0), COALESCE(tool_cost_usd, 0),
				COALESCE(platform_cost_usd, 0), COALESCE(review_cost_usd, 0),
				COALESCE(verification_cost_usd, 0), COALESCE(retry_count, 0)
			FROM sessions WHERE session_id = ?
	`, sessionID).Scan(
		&rec.SessionID, &rec.ActorSubject, &rec.Agent, &rec.Classification, &rec.PromptSHA256, &rec.Status, &createdAtStr,
		&rec.RunID, &rec.PermissionMode, &rec.ApprovalMode, &rec.WorkspaceMode,
		&rec.UseCaseID, &rec.WorkflowID, &rec.WorkItemID, &rec.WorkItemType, &rec.RepoURL, &rec.Branch, &rec.CommitSHA, &rec.Intent, &rec.ActorHint, &rec.SourceSystem,
		&rec.StoryPoints, &rec.EstimatedDevDays, &rec.BlendedDayRateUSD, &rec.BaselineCostUSD,
		&rec.ModelCostUSD, &rec.ToolCostUSD, &rec.PlatformCostUSD, &rec.ReviewCostUSD,
		&rec.VerificationCostUSD, &rec.RetryCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, fmt.Errorf("session not found")
	}
	if err != nil {
		return SessionRecord{}, fmt.Errorf("query session: %w", err)
	}
	rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	return rec, nil
}

func (s *SQLiteSessionStore) ListRecent(ctx context.Context, actorSubject string, limit int) ([]SessionRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
			SELECT
				session_id, actor_subject, agent, classification, prompt_sha256, status, created_at,
				COALESCE(run_id, ''), COALESCE(permission_mode, 'reviewed'), COALESCE(approval_mode, 'manual'), COALESCE(workspace_mode, ''),
				COALESCE(use_case_id, ''), COALESCE(workflow_id, ''), COALESCE(work_item_id, ''), COALESCE(work_item_type, ''),
				COALESCE(repo_url, ''), COALESCE(branch, ''), COALESCE(commit_sha, ''), COALESCE(intent, ''), COALESCE(actor_hint, ''), COALESCE(source_system, ''),
				COALESCE(story_points, 0), COALESCE(estimated_dev_days, 0), COALESCE(blended_day_rate_usd, 0),
				COALESCE(baseline_cost_usd, 0), COALESCE(model_cost_usd, 0), COALESCE(tool_cost_usd, 0),
				COALESCE(platform_cost_usd, 0), COALESCE(review_cost_usd, 0),
				COALESCE(verification_cost_usd, 0), COALESCE(retry_count, 0)
			FROM sessions
			WHERE actor_subject = ?
			ORDER BY created_at DESC
			LIMIT ?
	`, actorSubject, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var rec SessionRecord
		var createdAtStr string
		if err := rows.Scan(
			&rec.SessionID, &rec.ActorSubject, &rec.Agent, &rec.Classification, &rec.PromptSHA256, &rec.Status, &createdAtStr,
			&rec.RunID, &rec.PermissionMode, &rec.ApprovalMode, &rec.WorkspaceMode,
			&rec.UseCaseID, &rec.WorkflowID, &rec.WorkItemID, &rec.WorkItemType, &rec.RepoURL, &rec.Branch, &rec.CommitSHA, &rec.Intent, &rec.ActorHint, &rec.SourceSystem,
			&rec.StoryPoints, &rec.EstimatedDevDays, &rec.BlendedDayRateUSD, &rec.BaselineCostUSD,
			&rec.ModelCostUSD, &rec.ToolCostUSD, &rec.PlatformCostUSD, &rec.ReviewCostUSD,
			&rec.VerificationCostUSD, &rec.RetryCount,
		); err != nil {
			return nil, fmt.Errorf("scan recent session: %w", err)
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		sessions = append(sessions, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent sessions: %w", err)
	}
	return sessions, nil
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
