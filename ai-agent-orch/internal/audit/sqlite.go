package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
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

	db, err := sqlitex.Open(dbPath, "audit")
	if err != nil {
		return nil, err
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
	if err := s.ensureAuditColumn("parent_session_id", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureAuditColumn("prev_event_hash", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureAuditColumn("event_hash", "TEXT"); err != nil {
		return err
	}
	for column, definition := range map[string]string{
		"trust_level":                "TEXT",
		"enforcement_mode":           "TEXT",
		"provider":                   "TEXT",
		"model_alias":                "TEXT",
		"model_resolved":             "TEXT",
		"requested_model_alias":      "TEXT",
		"credential_source":          "TEXT",
		"reasoning_effort_requested": "TEXT",
		"reasoning_effort_applied":   "TEXT",
		"reasoning_source":           "TEXT",
		"gateway_backend":            "TEXT",
		"run_id":                     "TEXT",
		"work_item_id":               "TEXT",
		"repo_url":                   "TEXT",
		"branch":                     "TEXT",
		"commit_sha":                 "TEXT",
		"patch_id":                   "TEXT",
		"patch_buffer_id":            "TEXT",
		"patch_decision":             "TEXT",
		"patch_file_count":           "INTEGER NOT NULL DEFAULT 0",
		"runtime":                    "TEXT",
		"runtime_status":             "TEXT",
		"duration_ms":                "INTEGER NOT NULL DEFAULT 0",
		"event_count":                "INTEGER NOT NULL DEFAULT 0",
		"patch_count":                "INTEGER NOT NULL DEFAULT 0",
		"tool_call_count":            "INTEGER NOT NULL DEFAULT 0",
		"workspace_sha256":           "TEXT",
		"opencode_version":           "TEXT",
		"token_usage_json":           "TEXT",
	} {
		if err := s.ensureAuditColumn(column, definition); err != nil {
			return err
		}
	}
	_, err = s.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_audit_session ON audit_events(session_id);
		CREATE INDEX IF NOT EXISTS idx_audit_type ON audit_events(event_type);
		CREATE INDEX IF NOT EXISTS idx_audit_recorded ON audit_events(recorded_at);
		CREATE INDEX IF NOT EXISTS idx_audit_parent ON audit_events(parent_event_id);
		CREATE INDEX IF NOT EXISTS idx_audit_parent_session ON audit_events(parent_session_id);
		CREATE INDEX IF NOT EXISTS idx_audit_trust ON audit_events(trust_level);
		CREATE INDEX IF NOT EXISTS idx_audit_provider_model ON audit_events(provider, model_alias);
		CREATE INDEX IF NOT EXISTS idx_audit_run ON audit_events(run_id);
		CREATE INDEX IF NOT EXISTS idx_audit_patch ON audit_events(patch_id);
		CREATE TABLE IF NOT EXISTS audit_chain_heads (
			session_id TEXT PRIMARY KEY,
			head_hash TEXT NOT NULL
		);
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

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *SQLiteStore) Append(ctx context.Context, event Event) (Event, error) {
	return s.insertEvent(ctx, s.db, event)
}

func (s *SQLiteStore) insertEvent(ctx context.Context, execer sqlExecutor, event Event) (Event, error) {
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
	tokenUsageJSON, err := json.Marshal(event.TokenUsage)
	if err != nil {
		return Event{}, fmt.Errorf("encode token usage: %w", err)
	}

	// Store full event as JSON for schema flexibility.
	payloadJSON, err := json.Marshal(event)
	if err != nil {
		return Event{}, fmt.Errorf("encode payload: %w", err)
	}

	_, err = execer.ExecContext(ctx, `
			INSERT INTO audit_events (
				event_id, parent_event_id, parent_session_id, session_id, event_type, actor, agent, classification,
				reason, findings_json, prompt_sha256, estimated_cost_usd, cost_cap_usd,
				raw_prompt_stored, raw_response_stored, correlation_subject, recorded_at,
				prev_event_hash, event_hash,
				trust_level, enforcement_mode, provider, model_alias, model_resolved,
				requested_model_alias, credential_source, reasoning_effort_requested, reasoning_effort_applied, reasoning_source,
				gateway_backend,
				run_id, work_item_id, repo_url, branch, commit_sha, patch_id, patch_buffer_id, patch_decision, patch_file_count,
				runtime, runtime_status, duration_ms, event_count, patch_count, tool_call_count,
				workspace_sha256, opencode_version, token_usage_json, payload_json
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
		event.EventID, event.ParentEventID, event.ParentSessionID, event.SessionID, event.EventType, event.Actor, event.Agent,
		event.Classification, event.Reason, string(findingsJSON), event.PromptSHA256,
		event.EstimatedCostUSD, event.CostCapUSD,
		boolToInt(event.RawPromptStored), boolToInt(event.RawResponseStored),
		event.CorrelationSubject, event.RecordedAt.Format(time.RFC3339Nano),
		event.PrevEventHash, event.EventHash,
		event.TrustLevel, event.EnforcementMode, event.Provider, event.ModelAlias, event.ModelResolved,
		event.RequestedModelAlias, event.CredentialSource, event.ReasoningEffortRequested, event.ReasoningEffortApplied, event.ReasoningSource,
		event.GatewayBackend,
		event.RunID, event.WorkItemID, event.RepoURL, event.Branch, event.CommitSHA, event.PatchID, event.PatchBufferID, event.PatchDecision, event.PatchFileCount,
		event.Runtime, event.RuntimeStatus, event.DurationMS, event.EventCount, event.PatchCount, event.ToolCallCount,
		event.WorkspaceSHA256, event.OpencodeVersion, string(tokenUsageJSON), string(payloadJSON),
	)
	if err != nil {
		return Event{}, fmt.Errorf("insert audit event: %w", err)
	}

	return event, nil
}

// AppendIfPrev appends event only when the session chain head still matches
// expectedPrevHash. It gives ChainAppender an atomic compare-and-append
// primitive for SQLite-backed audit chains.
func (s *SQLiteStore) AppendIfPrev(ctx context.Context, event Event, expectedPrevHash string) (Event, bool, error) {
	if s == nil || s.db == nil {
		return Event{}, false, errors.New("sqlite audit store is required")
	}
	if event.SessionID == "" {
		persisted, err := s.Append(ctx, event)
		return persisted, err == nil, err
	}
	if event.PrevEventHash != expectedPrevHash {
		return Event{}, false, errors.New("event prev hash does not match expected previous hash")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, false, fmt.Errorf("begin audit chain append: %w", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	currentHead, err := latestSessionHashTx(ctx, tx, event.SessionID)
	if err != nil {
		return Event{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO audit_chain_heads (session_id, head_hash) VALUES (?, ?)
	`, event.SessionID, currentHead); err != nil {
		return Event{}, false, fmt.Errorf("seed audit chain head: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE audit_chain_heads SET head_hash = ? WHERE session_id = ? AND head_hash = ?
	`, event.EventHash, event.SessionID, expectedPrevHash)
	if err != nil {
		return Event{}, false, fmt.Errorf("update audit chain head: %w", err)
	}
	updated, err := res.RowsAffected()
	if err != nil {
		return Event{}, false, fmt.Errorf("audit chain head rows affected: %w", err)
	}
	if updated != 1 {
		return Event{}, false, nil
	}
	persisted, err := s.insertEvent(ctx, tx, event)
	if err != nil {
		return Event{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, false, fmt.Errorf("commit audit chain append: %w", err)
	}
	rollback = false
	return persisted, true, nil
}

func latestSessionHashTx(ctx context.Context, tx *sql.Tx, sessionID string) (string, error) {
	var latest string
	err := tx.QueryRowContext(ctx, `
		SELECT event_hash FROM audit_events
		WHERE session_id = ?
		ORDER BY recorded_at DESC, rowid DESC
		LIMIT 1
	`, sessionID).Scan(&latest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load audit chain head: %w", err)
	}
	return latest, nil
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
		ORDER BY recorded_at ASC, rowid ASC
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
	return s.ChainRetentionPolicy(ctx, maxAge)
}

// ChainRetentionPolicy removes only complete session chains whose newest event
// is outside the retention window. This preserves verification for any retained
// session history.
func (s *SQLiteStore) ChainRetentionPolicy(ctx context.Context, maxAge time.Duration) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("sqlite audit store is required")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cutoff := time.Now().UTC().Add(-maxAge).Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin chain retention: %w", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	var purged int64
	res, err := tx.ExecContext(ctx, `
		DELETE FROM audit_events
		WHERE (session_id IS NULL OR session_id = '') AND recorded_at < ?
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge unchained audit events: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("unchained rows affected: %w", err)
	}
	purged += n

	rows, err := tx.QueryContext(ctx, `
		SELECT session_id FROM audit_events
		WHERE session_id IS NOT NULL AND session_id <> ''
		GROUP BY session_id
		HAVING MAX(recorded_at) < ?
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("query expired audit chains: %w", err)
	}
	var expired []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan expired audit chain: %w", err)
		}
		expired = append(expired, sessionID)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close expired audit chain rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate expired audit chains: %w", err)
	}
	for _, sessionID := range expired {
		res, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE session_id = ?`, sessionID)
		if err != nil {
			return 0, fmt.Errorf("purge audit chain %s: %w", sessionID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("chain rows affected: %w", err)
		}
		purged += n
		if _, err := tx.ExecContext(ctx, `DELETE FROM audit_chain_heads WHERE session_id = ?`, sessionID); err != nil {
			return 0, fmt.Errorf("purge audit chain head %s: %w", sessionID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit chain retention: %w", err)
	}
	rollback = false
	return purged, nil
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
