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

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
)

// SessionRecord is the durable representation of a user session.
type SessionRecord struct {
	SessionID                 string
	ParentSessionID           string
	ActorSubject              string
	Agent                     string
	RoutedAgent               string
	RoutingConfidence         string
	HumanConfirmationRequired bool
	Classification            string
	PromptSHA256              string
	Status                    string // created, awaiting_confirmation, confirming, confirmed, running, done, failed, confirm_failed, aborted
	CreatedAt                 time.Time
	// GatewayTokenSHA256 is the hash of the per-session secret a runtime must
	// present on model gateway calls. Empty means the session predates token
	// binding and is accepted with the shared runtime token alone.
	GatewayTokenSHA256 string
	// RuntimeGatewayTokenSHA256 is the hash of the secret minted at dispatch
	// time for server-side runtimes (the ACP lane). The client token above
	// stays valid; the gateway accepts either.
	RuntimeGatewayTokenSHA256 string
	// Governed runtime binding fields.
	RunID          string
	PermissionMode string
	ApprovalMode   string
	WorkspaceMode  string
	// Control-plane binding fields.
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
	// ClientSessionID correlates an auto-created gateway session with the
	// caller's own conversation id (for example an OpenCode session id), so a
	// single conversation reuses one governed session instead of minting one
	// per model call. Empty for wrapper-created and control-plane sessions.
	ClientSessionID string
	// Claimed* fields are client-asserted machine identity from a managed
	// client's optional client_identity block (see managed_client.go). They
	// are CLAIMED evidence, never a security principal or authorization
	// input, and are frozen at session creation except for a fill-in-once
	// update that never overwrites a value already recorded.
	ClaimedOSUsername  string
	ClaimedHostname    string
	ClaimedGithubLogin string
	// Cost/value sizing.
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
	db, err := sqlitex.Open(dbPath, "session")
	if err != nil {
		return nil, err
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
	// Migrations: add control-plane binding and sizing columns.
	cols := map[string]string{
		"parent_session_id":            "TEXT NOT NULL DEFAULT ''",
		"run_id":                       "TEXT NOT NULL DEFAULT ''",
		"permission_mode":              "TEXT NOT NULL DEFAULT 'reviewed'",
		"approval_mode":                "TEXT NOT NULL DEFAULT 'manual'",
		"workspace_mode":               "TEXT NOT NULL DEFAULT ''",
		"use_case_id":                  "TEXT NOT NULL DEFAULT ''",
		"workflow_id":                  "TEXT NOT NULL DEFAULT ''",
		"work_item_id":                 "TEXT NOT NULL DEFAULT ''",
		"work_item_type":               "TEXT NOT NULL DEFAULT ''",
		"repo_url":                     "TEXT NOT NULL DEFAULT ''",
		"branch":                       "TEXT NOT NULL DEFAULT ''",
		"commit_sha":                   "TEXT NOT NULL DEFAULT ''",
		"intent":                       "TEXT NOT NULL DEFAULT ''",
		"actor_hint":                   "TEXT NOT NULL DEFAULT ''",
		"source_system":                "TEXT NOT NULL DEFAULT ''",
		"story_points":                 "INTEGER NOT NULL DEFAULT 0",
		"estimated_dev_days":           "REAL NOT NULL DEFAULT 0",
		"blended_day_rate_usd":         "REAL NOT NULL DEFAULT 0",
		"baseline_cost_usd":            "REAL NOT NULL DEFAULT 0",
		"model_cost_usd":               "REAL NOT NULL DEFAULT 0",
		"tool_cost_usd":                "REAL NOT NULL DEFAULT 0",
		"platform_cost_usd":            "REAL NOT NULL DEFAULT 0",
		"review_cost_usd":              "REAL NOT NULL DEFAULT 0",
		"verification_cost_usd":        "REAL NOT NULL DEFAULT 0",
		"retry_count":                  "INTEGER NOT NULL DEFAULT 0",
		"gateway_token_sha256":         "TEXT NOT NULL DEFAULT ''",
		"runtime_gateway_token_sha256": "TEXT NOT NULL DEFAULT ''",
		"routed_agent":                 "TEXT NOT NULL DEFAULT ''",
		"routing_confidence":           "TEXT NOT NULL DEFAULT ''",
		"human_confirmation_required":  "INTEGER NOT NULL DEFAULT 0",
		"client_session_id":            "TEXT NOT NULL DEFAULT ''",
		"claimed_os_username":          "TEXT NOT NULL DEFAULT ''",
		"claimed_hostname":             "TEXT NOT NULL DEFAULT ''",
		"claimed_github_login":         "TEXT NOT NULL DEFAULT ''",
	}
	for col, def := range cols {
		if err := s.ensureSessionColumn(col, def); err != nil {
			return err
		}
	}
	// Index supports auto-session reuse lookups keyed by the caller's
	// conversation id within an actor.
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_actor_client ON sessions(actor_subject, client_session_id)`); err != nil {
		return fmt.Errorf("create sessions actor/client index: %w", err)
	}
	return nil
}

func (s *SQLiteSessionStore) ensureSessionColumn(column string, definition string) error {
	columns, err := sqlitex.TableColumns(s.db, "sessions")
	if err != nil {
		return fmt.Errorf("inspect sessions schema: %w", err)
	}
	if columns[column] {
		return nil
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
			session_id, parent_session_id, actor_subject, agent, routed_agent, routing_confidence, human_confirmation_required, classification, prompt_sha256, status, created_at,
			run_id, permission_mode, approval_mode, workspace_mode,
			use_case_id, workflow_id, work_item_id, work_item_type, repo_url, branch, commit_sha, intent, actor_hint, source_system,
			story_points, estimated_dev_days, blended_day_rate_usd, baseline_cost_usd,
			model_cost_usd, tool_cost_usd, platform_cost_usd, review_cost_usd,
			verification_cost_usd, retry_count, gateway_token_sha256, runtime_gateway_token_sha256, client_session_id,
			claimed_os_username, claimed_hostname, claimed_github_login
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?)
	`,
		rec.SessionID, rec.ParentSessionID, rec.ActorSubject, rec.Agent, rec.RoutedAgent, rec.RoutingConfidence, boolToInt(rec.HumanConfirmationRequired), rec.Classification, rec.PromptSHA256, rec.Status, rec.CreatedAt.Format(time.RFC3339Nano),
		rec.RunID, rec.PermissionMode, rec.ApprovalMode, rec.WorkspaceMode,
		rec.UseCaseID, rec.WorkflowID, rec.WorkItemID, rec.WorkItemType, rec.RepoURL, rec.Branch, rec.CommitSHA, rec.Intent, rec.ActorHint, rec.SourceSystem,
		rec.StoryPoints, rec.EstimatedDevDays, rec.BlendedDayRateUSD, rec.BaselineCostUSD,
		rec.ModelCostUSD, rec.ToolCostUSD, rec.PlatformCostUSD, rec.ReviewCostUSD,
		rec.VerificationCostUSD, rec.RetryCount, rec.GatewayTokenSHA256, rec.RuntimeGatewayTokenSHA256, rec.ClientSessionID,
		rec.ClaimedOSUsername, rec.ClaimedHostname, rec.ClaimedGithubLogin,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *SQLiteSessionStore) Get(ctx context.Context, sessionID string) (SessionRecord, error) {
	var rec SessionRecord
	var createdAtStr string
	var humanConfirmationRequired int
	err := s.db.QueryRowContext(ctx, `
			SELECT
				session_id, COALESCE(parent_session_id, ''), actor_subject, agent, COALESCE(routed_agent, ''), COALESCE(routing_confidence, ''), COALESCE(human_confirmation_required, 0), classification, prompt_sha256, status, created_at,
				COALESCE(run_id, ''), COALESCE(permission_mode, 'reviewed'), COALESCE(approval_mode, 'manual'), COALESCE(workspace_mode, ''),
				COALESCE(use_case_id, ''), COALESCE(workflow_id, ''), COALESCE(work_item_id, ''), COALESCE(work_item_type, ''),
				COALESCE(repo_url, ''), COALESCE(branch, ''), COALESCE(commit_sha, ''), COALESCE(intent, ''), COALESCE(actor_hint, ''), COALESCE(source_system, ''),
				COALESCE(story_points, 0), COALESCE(estimated_dev_days, 0), COALESCE(blended_day_rate_usd, 0),
				COALESCE(baseline_cost_usd, 0), COALESCE(model_cost_usd, 0), COALESCE(tool_cost_usd, 0),
				COALESCE(platform_cost_usd, 0), COALESCE(review_cost_usd, 0),
				COALESCE(verification_cost_usd, 0), COALESCE(retry_count, 0), COALESCE(gateway_token_sha256, ''), COALESCE(runtime_gateway_token_sha256, ''), COALESCE(client_session_id, ''),
				COALESCE(claimed_os_username, ''), COALESCE(claimed_hostname, ''), COALESCE(claimed_github_login, '')
			FROM sessions WHERE session_id = ?
	`, sessionID).Scan(
		&rec.SessionID, &rec.ParentSessionID, &rec.ActorSubject, &rec.Agent, &rec.RoutedAgent, &rec.RoutingConfidence, &humanConfirmationRequired, &rec.Classification, &rec.PromptSHA256, &rec.Status, &createdAtStr,
		&rec.RunID, &rec.PermissionMode, &rec.ApprovalMode, &rec.WorkspaceMode,
		&rec.UseCaseID, &rec.WorkflowID, &rec.WorkItemID, &rec.WorkItemType, &rec.RepoURL, &rec.Branch, &rec.CommitSHA, &rec.Intent, &rec.ActorHint, &rec.SourceSystem,
		&rec.StoryPoints, &rec.EstimatedDevDays, &rec.BlendedDayRateUSD, &rec.BaselineCostUSD,
		&rec.ModelCostUSD, &rec.ToolCostUSD, &rec.PlatformCostUSD, &rec.ReviewCostUSD,
		&rec.VerificationCostUSD, &rec.RetryCount, &rec.GatewayTokenSHA256, &rec.RuntimeGatewayTokenSHA256, &rec.ClientSessionID,
		&rec.ClaimedOSUsername, &rec.ClaimedHostname, &rec.ClaimedGithubLogin,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, fmt.Errorf("session not found")
	}
	if err != nil {
		return SessionRecord{}, fmt.Errorf("query session: %w", err)
	}
	rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	rec.HumanConfirmationRequired = humanConfirmationRequired != 0
	return rec, nil
}

// clientSessionReuseWindow bounds how long a client conversation id keeps
// reusing the same governed session. Past it, a new session is created so stale
// or abandoned conversation ids cannot bind new traffic to old context.
const clientSessionReuseWindow = 12 * time.Hour

// FindActiveByActorClientSession returns the most recent non-terminal session
// for the given actor and caller conversation id, created within the reuse
// window. It powers auto-session reuse so one client conversation maps to one
// governed session instead of one per model call. Returns ok=false when no
// active, recent match exists.
func (s *SQLiteSessionStore) FindActiveByActorClientSession(ctx context.Context, actorSubject, clientSessionID string) (SessionRecord, bool, error) {
	actorSubject = strings.TrimSpace(actorSubject)
	clientSessionID = strings.TrimSpace(clientSessionID)
	if actorSubject == "" || clientSessionID == "" {
		return SessionRecord{}, false, nil
	}
	// Bound reuse to a recent window so a stale or abandoned client conversation
	// id cannot bind new traffic to an old governed session indefinitely. Past
	// the window, a fresh session is created for the same client id.
	cutoff := time.Now().UTC().Add(-clientSessionReuseWindow).Format(time.RFC3339Nano)
	var sessionID string
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id FROM sessions
		WHERE actor_subject = ? AND client_session_id = ?
			AND status NOT IN ('done', 'failed', 'confirm_failed', 'aborted')
			AND created_at > ?
		ORDER BY created_at DESC
		LIMIT 1
	`, actorSubject, clientSessionID, cutoff).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, false, nil
	}
	if err != nil {
		return SessionRecord{}, false, fmt.Errorf("find session by client id: %w", err)
	}
	rec, err := s.Get(ctx, sessionID)
	if err != nil {
		return SessionRecord{}, false, err
	}
	return rec, true, nil
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
				session_id, COALESCE(parent_session_id, ''), actor_subject, agent, COALESCE(routed_agent, ''), classification, prompt_sha256, status, created_at,
				COALESCE(run_id, ''), COALESCE(permission_mode, 'reviewed'), COALESCE(approval_mode, 'manual'), COALESCE(workspace_mode, ''),
				COALESCE(use_case_id, ''), COALESCE(workflow_id, ''), COALESCE(work_item_id, ''), COALESCE(work_item_type, ''),
				COALESCE(repo_url, ''), COALESCE(branch, ''), COALESCE(commit_sha, ''), COALESCE(intent, ''), COALESCE(actor_hint, ''), COALESCE(source_system, ''),
				COALESCE(story_points, 0), COALESCE(estimated_dev_days, 0), COALESCE(blended_day_rate_usd, 0),
				COALESCE(baseline_cost_usd, 0), COALESCE(model_cost_usd, 0), COALESCE(tool_cost_usd, 0),
				COALESCE(platform_cost_usd, 0), COALESCE(review_cost_usd, 0),
				COALESCE(verification_cost_usd, 0), COALESCE(retry_count, 0), COALESCE(gateway_token_sha256, ''), COALESCE(runtime_gateway_token_sha256, '')
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
			&rec.SessionID, &rec.ParentSessionID, &rec.ActorSubject, &rec.Agent, &rec.RoutedAgent, &rec.Classification, &rec.PromptSHA256, &rec.Status, &createdAtStr,
			&rec.RunID, &rec.PermissionMode, &rec.ApprovalMode, &rec.WorkspaceMode,
			&rec.UseCaseID, &rec.WorkflowID, &rec.WorkItemID, &rec.WorkItemType, &rec.RepoURL, &rec.Branch, &rec.CommitSHA, &rec.Intent, &rec.ActorHint, &rec.SourceSystem,
			&rec.StoryPoints, &rec.EstimatedDevDays, &rec.BlendedDayRateUSD, &rec.BaselineCostUSD,
			&rec.ModelCostUSD, &rec.ToolCostUSD, &rec.PlatformCostUSD, &rec.ReviewCostUSD,
			&rec.VerificationCostUSD, &rec.RetryCount, &rec.GatewayTokenSHA256, &rec.RuntimeGatewayTokenSHA256,
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

// ListRecentSince returns an actor's sessions created at or after since,
// newest first, capped at limit. It powers actor-scoped, time-windowed
// reporting (the governance insight projection) where ListRecent's smaller
// pagination cap is too tight for a multi-day window.
func (s *SQLiteSessionStore) ListRecentSince(ctx context.Context, actorSubject string, since time.Time, limit int) ([]SessionRecord, error) {
	if limit <= 0 || limit > insightSessionCap {
		limit = insightSessionCap
	}
	rows, err := s.db.QueryContext(ctx, `
			SELECT
				session_id, COALESCE(parent_session_id, ''), actor_subject, agent, COALESCE(routed_agent, ''), classification, prompt_sha256, status, created_at,
				COALESCE(run_id, ''), COALESCE(permission_mode, 'reviewed'), COALESCE(approval_mode, 'manual'), COALESCE(workspace_mode, ''),
				COALESCE(use_case_id, ''), COALESCE(workflow_id, ''), COALESCE(work_item_id, ''), COALESCE(work_item_type, ''),
				COALESCE(repo_url, ''), COALESCE(branch, ''), COALESCE(commit_sha, ''), COALESCE(intent, ''), COALESCE(actor_hint, ''), COALESCE(source_system, ''),
				COALESCE(story_points, 0), COALESCE(estimated_dev_days, 0), COALESCE(blended_day_rate_usd, 0),
				COALESCE(baseline_cost_usd, 0), COALESCE(model_cost_usd, 0), COALESCE(tool_cost_usd, 0),
				COALESCE(platform_cost_usd, 0), COALESCE(review_cost_usd, 0),
				COALESCE(verification_cost_usd, 0), COALESCE(retry_count, 0), COALESCE(gateway_token_sha256, ''), COALESCE(runtime_gateway_token_sha256, '')
			FROM sessions
			WHERE actor_subject = ? AND created_at >= ?
			ORDER BY created_at DESC
			LIMIT ?
	`, actorSubject, since.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, fmt.Errorf("list recent sessions since: %w", err)
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var rec SessionRecord
		var createdAtStr string
		if err := rows.Scan(
			&rec.SessionID, &rec.ParentSessionID, &rec.ActorSubject, &rec.Agent, &rec.RoutedAgent, &rec.Classification, &rec.PromptSHA256, &rec.Status, &createdAtStr,
			&rec.RunID, &rec.PermissionMode, &rec.ApprovalMode, &rec.WorkspaceMode,
			&rec.UseCaseID, &rec.WorkflowID, &rec.WorkItemID, &rec.WorkItemType, &rec.RepoURL, &rec.Branch, &rec.CommitSHA, &rec.Intent, &rec.ActorHint, &rec.SourceSystem,
			&rec.StoryPoints, &rec.EstimatedDevDays, &rec.BlendedDayRateUSD, &rec.BaselineCostUSD,
			&rec.ModelCostUSD, &rec.ToolCostUSD, &rec.PlatformCostUSD, &rec.ReviewCostUSD,
			&rec.VerificationCostUSD, &rec.RetryCount, &rec.GatewayTokenSHA256, &rec.RuntimeGatewayTokenSHA256,
		); err != nil {
			return nil, fmt.Errorf("scan recent session since: %w", err)
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		sessions = append(sessions, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent sessions since: %w", err)
	}
	return sessions, nil
}

// ListRecentAll returns recent sessions across every actor for admin
// oversight views. Ordinary listing stays actor-scoped.
func (s *SQLiteSessionStore) ListRecentAll(ctx context.Context, limit int) ([]SessionRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
			SELECT
				session_id, COALESCE(parent_session_id, ''), actor_subject, agent, COALESCE(routed_agent, ''), classification, prompt_sha256, status, created_at,
				COALESCE(run_id, ''), COALESCE(permission_mode, 'reviewed'), COALESCE(approval_mode, 'manual'), COALESCE(workspace_mode, ''),
				COALESCE(use_case_id, ''), COALESCE(workflow_id, ''), COALESCE(work_item_id, ''), COALESCE(work_item_type, ''),
				COALESCE(repo_url, ''), COALESCE(branch, ''), COALESCE(commit_sha, ''), COALESCE(intent, ''), COALESCE(actor_hint, ''), COALESCE(source_system, ''),
				COALESCE(story_points, 0), COALESCE(estimated_dev_days, 0), COALESCE(blended_day_rate_usd, 0),
				COALESCE(baseline_cost_usd, 0), COALESCE(model_cost_usd, 0), COALESCE(tool_cost_usd, 0),
				COALESCE(platform_cost_usd, 0), COALESCE(review_cost_usd, 0),
				COALESCE(verification_cost_usd, 0), COALESCE(retry_count, 0), COALESCE(gateway_token_sha256, ''), COALESCE(runtime_gateway_token_sha256, '')
			FROM sessions
			ORDER BY created_at DESC
			LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list all sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionRecord
	for rows.Next() {
		var rec SessionRecord
		var createdAtStr string
		if err := rows.Scan(
			&rec.SessionID, &rec.ParentSessionID, &rec.ActorSubject, &rec.Agent, &rec.RoutedAgent, &rec.Classification, &rec.PromptSHA256, &rec.Status, &createdAtStr,
			&rec.RunID, &rec.PermissionMode, &rec.ApprovalMode, &rec.WorkspaceMode,
			&rec.UseCaseID, &rec.WorkflowID, &rec.WorkItemID, &rec.WorkItemType, &rec.RepoURL, &rec.Branch, &rec.CommitSHA, &rec.Intent, &rec.ActorHint, &rec.SourceSystem,
			&rec.StoryPoints, &rec.EstimatedDevDays, &rec.BlendedDayRateUSD, &rec.BaselineCostUSD,
			&rec.ModelCostUSD, &rec.ToolCostUSD, &rec.PlatformCostUSD, &rec.ReviewCostUSD,
			&rec.VerificationCostUSD, &rec.RetryCount, &rec.GatewayTokenSHA256, &rec.RuntimeGatewayTokenSHA256,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		sessions = append(sessions, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

// ListAllSince returns sessions for export, newest first, optionally bounded
// by a creation time. The cap keeps a single export request from streaming an
// unbounded result set; callers paginate with `since` if they hit it.
func (s *SQLiteSessionStore) ListAllSince(ctx context.Context, since time.Time, max int) ([]SessionRecord, error) {
	if max <= 0 || max > 5000 {
		max = 5000
	}
	query := `SELECT session_id FROM sessions ORDER BY created_at DESC LIMIT ?`
	args := []any{max}
	if !since.IsZero() {
		query = `SELECT session_id FROM sessions WHERE created_at >= ? ORDER BY created_at DESC LIMIT ?`
		args = []any{since.Format(time.RFC3339Nano), max}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list export sessions: %w", err)
	}
	ids := make([]string, 0, 64)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan export session id: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export session ids: %w", err)
	}
	records := make([]SessionRecord, 0, len(ids))
	for _, id := range ids {
		rec, err := s.Get(ctx, id)
		if err != nil {
			continue
		}
		records = append(records, rec)
	}
	return records, nil
}

// SetRuntimeGatewayTokenHash stores the hash of the dispatch-time runtime
// gateway secret. Discovered via interface assertion so alternative session
// stores without runtime-token support keep compiling.
func (s *SQLiteSessionStore) SetRuntimeGatewayTokenHash(ctx context.Context, sessionID, hash string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET runtime_gateway_token_sha256 = ? WHERE session_id = ?`, hash, sessionID)
	if err != nil {
		return fmt.Errorf("set runtime gateway token: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

// UpdateClaimedIdentityIfEmpty fills in claimed machine-identity fields on an
// existing session, one field at a time, only where the stored value is
// still empty. Managed-client sessions are create-once, so identity is
// normally frozen at creation; this lets a later batch fill in fields the
// first batch omitted, without ever overwriting a value already recorded.
// Passing an empty string for a field is a no-op for that field.
func (s *SQLiteSessionStore) UpdateClaimedIdentityIfEmpty(ctx context.Context, sessionID, osUsername, hostname, githubLogin string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET
			claimed_os_username = CASE WHEN claimed_os_username = '' THEN ? ELSE claimed_os_username END,
			claimed_hostname = CASE WHEN claimed_hostname = '' THEN ? ELSE claimed_hostname END,
			claimed_github_login = CASE WHEN claimed_github_login = '' THEN ? ELSE claimed_github_login END
		WHERE session_id = ?
	`, osUsername, hostname, githubLogin, sessionID)
	if err != nil {
		return fmt.Errorf("update claimed identity: %w", err)
	}
	return nil
}

// IdentityMapRow is one grouped row of the admin identity-map surface: a
// distinct (actor, claimed OS username, claimed GitHub login) combination
// seen across a managed client's sessions, for downstream fleet/metrics
// correlation. It carries CLAIMED evidence only, never an authorization
// input.
type IdentityMapRow struct {
	ActorSubject       string
	ClaimedOSUsername  string
	ClaimedGithubLogin string
	ClaimedHostname    string
	LastSeen           time.Time
}

// IdentityMap groups sessions by (actor_subject, claimed_os_username,
// claimed_github_login) and returns one row per group with the most recent
// session's hostname and creation time. Sessions in any status are included,
// including done, because test-connection enrolment sessions end
// immediately; rows with an empty claimed_os_username are excluded since
// there is nothing claimed to correlate.
func (s *SQLiteSessionStore) IdentityMap(ctx context.Context) ([]IdentityMapRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT actor_subject, COALESCE(claimed_os_username, ''), COALESCE(claimed_github_login, ''), COALESCE(claimed_hostname, ''), created_at
		FROM sessions
		WHERE COALESCE(claimed_os_username, '') <> ''
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query identity map: %w", err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	var result []IdentityMapRow
	for rows.Next() {
		var actorSubject, osUsername, githubLogin, hostname, createdAtStr string
		if err := rows.Scan(&actorSubject, &osUsername, &githubLogin, &hostname, &createdAtStr); err != nil {
			return nil, fmt.Errorf("scan identity map row: %w", err)
		}
		key := actorSubject + "\x00" + osUsername + "\x00" + githubLogin
		if seen[key] {
			continue
		}
		seen[key] = true
		createdAt, _ := time.Parse(time.RFC3339Nano, createdAtStr)
		result = append(result, IdentityMapRow{
			ActorSubject:       actorSubject,
			ClaimedOSUsername:  osUsername,
			ClaimedGithubLogin: githubLogin,
			ClaimedHostname:    hostname,
			LastSeen:           createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate identity map rows: %w", err)
	}
	return result, nil
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

func (s *SQLiteSessionStore) SetRoutedAgent(ctx context.Context, sessionID, agent string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET routed_agent = ? WHERE session_id = ?`, agent, sessionID)
	if err != nil {
		return fmt.Errorf("set routed agent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

func (s *SQLiteSessionStore) SetRouteDecision(ctx context.Context, sessionID, agent, routingConfidence string, humanConfirmationRequired bool) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET routed_agent = ?, routing_confidence = ?, human_confirmation_required = ?
		WHERE session_id = ?
	`, agent, routingConfidence, boolToInt(humanConfirmationRequired), sessionID)
	if err != nil {
		return fmt.Errorf("set route decision: %w", err)
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
