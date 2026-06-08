package governance

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DurableRegistryStore is a SQLite-backed store for use-cases, workflows,
// context manifests, cache outcomes, evidence records and maturity exports.
// It satisfies the same interface expectations as RegistryStore and can
// replace it in multi-user or restart-sensitive deployments.
type DurableRegistryStore struct {
	dbPath string
	db     *sql.DB
	now    func() time.Time
}

// NewDurableRegistryStore opens or creates a SQLite registry database.
func NewDurableRegistryStore(dbPath string) (*DurableRegistryStore, error) {
	if dbPath == "" {
		return nil, errors.New("registry db path is required")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create registry db directory: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open registry db: %w", err)
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
		return nil, fmt.Errorf("configure registry db: %w", err)
	}
	s := &DurableRegistryStore{
		dbPath: dbPath,
		db:     db,
		now:    func() time.Time { return time.Now().UTC() },
	}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate registry db: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set registry db permissions: %w", err)
	}
	return s, nil
}

func (s *DurableRegistryStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS use_cases (
			id TEXT PRIMARY KEY,
			owner TEXT NOT NULL,
			domain TEXT NOT NULL,
			expected_benefit TEXT NOT NULL,
			linked_work_item TEXT,
			classification TEXT NOT NULL,
			risk_level TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS workflows (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			stages_json TEXT,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS context_manifests (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			summary TEXT NOT NULL,
			source_system TEXT NOT NULL,
			source_object_id TEXT NOT NULL,
			source_path TEXT,
			actor TEXT NOT NULL,
			auth_scope TEXT NOT NULL,
			fetched_at TEXT NOT NULL,
			freshness TEXT,
			classification TEXT NOT NULL,
			cache_status TEXT NOT NULL,
			chunk_hashes_json TEXT,
			influenced_model INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS cache_outcomes (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			cache_scope TEXT NOT NULL,
			cache_key_hash TEXT NOT NULL,
			hit INTEGER NOT NULL,
			eligibility_reason TEXT,
			invalidation_reason TEXT,
			ttl_seconds INTEGER,
			source_freshness TEXT,
			classification TEXT,
			actor TEXT,
			repository TEXT,
			workflow TEXT,
			estimated_savings_usd REAL,
			avoided_tokens INTEGER,
			avoided_calls INTEGER,
			recorded_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS evidence_records (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			evidence_type TEXT NOT NULL,
			description TEXT NOT NULL,
			test_result TEXT,
			quality_system_link TEXT,
			security_finding TEXT,
			approval_receipt TEXT,
			patch_decision TEXT,
			external_ticket TEXT,
			trust_level TEXT,
			enforcement_mode TEXT,
			recorded_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS maturity_exports (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT,
			event_type TEXT NOT NULL,
			actor TEXT,
			team TEXT,
			use_case_id TEXT,
			workflow_id TEXT,
			work_item_id TEXT,
			repository TEXT,
			branch TEXT,
			commit_hash TEXT,
			classification TEXT,
			risk_level TEXT,
			agent TEXT,
			specialist TEXT,
			runtime TEXT,
			model_alias TEXT,
			model_resolved TEXT,
			final_status TEXT,
			policy_decision TEXT,
			policy_reason TEXT,
			secret_scan_result TEXT,
			kill_switch_result TEXT,
			oauth_failure_result TEXT,
			tool_loop_cap_result TEXT,
			cost_cap_result TEXT,
			human_gate_decision TEXT,
			baseline_cost_usd REAL,
			model_cost_usd REAL,
			tool_cost_usd REAL,
			platform_cost_usd REAL,
			review_cost_usd REAL,
			verification_cost_usd REAL,
			retry_count INTEGER,
			context_manifest_id TEXT,
			evidence_links_json TEXT,
			patch_decision TEXT,
			quality_result TEXT,
			evidence_completeness REAL,
			cycle_time_seconds REAL,
			blocked_event_category TEXT,
			cache_hits INTEGER,
			cache_misses INTEGER,
			cache_savings_estimate REAL,
			recorded_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_manifests_session ON context_manifests(session_id);
		CREATE INDEX IF NOT EXISTS idx_cache_session ON cache_outcomes(session_id);
		CREATE INDEX IF NOT EXISTS idx_evidence_session ON evidence_records(session_id);
		CREATE INDEX IF NOT EXISTS idx_maturity_session ON maturity_exports(session_id);
		`)
	if err != nil {
		return err
	}
	if err := s.ensureColumn("evidence_records", "trust_level", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("evidence_records", "enforcement_mode", "TEXT"); err != nil {
		return err
	}
	return nil
}

func (s *DurableRegistryStore) ensureColumn(table string, column string, definition string) error {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("inspect %s schema: %w", table, err)
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
			return fmt.Errorf("scan %s schema: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s schema: %w", table, err)
	}
	if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition)); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return nil
		}
		return fmt.Errorf("add %s column %s: %w", table, column, err)
	}
	return nil
}

// UseCase methods.

func (s *DurableRegistryStore) RegisterUseCase(uc UseCase) error {
	if uc.ID == "" || uc.Owner == "" || uc.Domain == "" || uc.Classification == "" || uc.RiskLevel == "" {
		return errors.New("use_case validation failed")
	}
	if uc.CreatedAt.IsZero() {
		uc.CreatedAt = s.now().UTC()
	}
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO use_cases (id, owner, domain, expected_benefit, linked_work_item, classification, risk_level, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, uc.ID, uc.Owner, uc.Domain, uc.ExpectedBenefit, uc.LinkedWorkItem, uc.Classification, uc.RiskLevel, uc.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *DurableRegistryStore) ListUseCases() ([]UseCase, error) {
	rows, err := s.db.Query(`SELECT id, owner, domain, expected_benefit, linked_work_item, classification, risk_level, created_at FROM use_cases ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UseCase
	for rows.Next() {
		var uc UseCase
		var createdAtStr string
		if err := rows.Scan(&uc.ID, &uc.Owner, &uc.Domain, &uc.ExpectedBenefit, &uc.LinkedWorkItem, &uc.Classification, &uc.RiskLevel, &createdAtStr); err != nil {
			return nil, err
		}
		uc.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		out = append(out, uc)
	}
	return out, rows.Err()
}

func (s *DurableRegistryStore) GetUseCase(id string) (UseCase, bool) {
	var uc UseCase
	var createdAtStr string
	err := s.db.QueryRow(`
		SELECT id, owner, domain, expected_benefit, linked_work_item, classification, risk_level, created_at
		FROM use_cases WHERE id = ?
	`, id).Scan(&uc.ID, &uc.Owner, &uc.Domain, &uc.ExpectedBenefit, &uc.LinkedWorkItem, &uc.Classification, &uc.RiskLevel, &createdAtStr)
	if err != nil {
		return UseCase{}, false
	}
	uc.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	return uc, true
}

// Workflow methods.

func (s *DurableRegistryStore) RegisterWorkflow(wf Workflow) error {
	if wf.ID == "" || wf.Name == "" {
		return errors.New("workflow validation failed")
	}
	if wf.CreatedAt.IsZero() {
		wf.CreatedAt = s.now().UTC()
	}
	stagesJSON, _ := json.Marshal(wf.Stages)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO workflows (id, name, description, stages_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, wf.ID, wf.Name, wf.Description, string(stagesJSON), wf.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *DurableRegistryStore) ListWorkflows() ([]Workflow, error) {
	rows, err := s.db.Query(`SELECT id, name, description, stages_json, created_at FROM workflows ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workflow
	for rows.Next() {
		var wf Workflow
		var stagesJSON string
		var createdAtStr string
		if err := rows.Scan(&wf.ID, &wf.Name, &wf.Description, &stagesJSON, &createdAtStr); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(stagesJSON), &wf.Stages)
		wf.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		out = append(out, wf)
	}
	return out, rows.Err()
}

func (s *DurableRegistryStore) GetWorkflow(id string) (Workflow, bool) {
	var wf Workflow
	var stagesJSON string
	var createdAtStr string
	err := s.db.QueryRow(`
		SELECT id, name, description, stages_json, created_at FROM workflows WHERE id = ?
	`, id).Scan(&wf.ID, &wf.Name, &wf.Description, &stagesJSON, &createdAtStr)
	if err != nil {
		return Workflow{}, false
	}
	_ = json.Unmarshal([]byte(stagesJSON), &wf.Stages)
	wf.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	return wf, true
}

// ContextManifest methods.

func (s *DurableRegistryStore) CreateManifest(m ContextManifest) error {
	if m.ID == "" {
		return errors.New("manifest id is required")
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = s.now().UTC()
	}
	chunkHashesJSON, _ := json.Marshal(m.ChunkHashes)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO context_manifests (
			id, session_id, summary, source_system, source_object_id, source_path,
			actor, auth_scope, fetched_at, freshness, classification, cache_status,
			chunk_hashes_json, influenced_model, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.ID, m.SessionID, m.Summary, m.SourceSystem, m.SourceObjectID, m.SourcePath,
		m.Actor, m.AuthScope, m.FetchedAt.Format(time.RFC3339Nano), m.Freshness, m.Classification, m.CacheStatus,
		string(chunkHashesJSON), boolToInt(m.InfluencedModel), m.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *DurableRegistryStore) GetManifest(id string) (ContextManifest, bool) {
	var m ContextManifest
	var chunkHashesJSON string
	var fetchedAtStr, createdAtStr string
	var influencedModel int
	err := s.db.QueryRow(`
		SELECT id, session_id, summary, source_system, source_object_id, source_path,
			actor, auth_scope, fetched_at, freshness, classification, cache_status,
			chunk_hashes_json, influenced_model, created_at
		FROM context_manifests WHERE id = ?
	`, id).Scan(&m.ID, &m.SessionID, &m.Summary, &m.SourceSystem, &m.SourceObjectID, &m.SourcePath,
		&m.Actor, &m.AuthScope, &fetchedAtStr, &m.Freshness, &m.Classification, &m.CacheStatus,
		&chunkHashesJSON, &influencedModel, &createdAtStr)
	if err != nil {
		return ContextManifest{}, false
	}
	_ = json.Unmarshal([]byte(chunkHashesJSON), &m.ChunkHashes)
	m.InfluencedModel = influencedModel != 0
	m.FetchedAt, _ = time.Parse(time.RFC3339Nano, fetchedAtStr)
	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	return m, true
}

// CacheOutcome methods.

func (s *DurableRegistryStore) AppendCacheOutcome(c CacheOutcome) error {
	if c.RecordedAt.IsZero() {
		c.RecordedAt = s.now().UTC()
	}
	_, err := s.db.Exec(`
		INSERT INTO cache_outcomes (
			id, session_id, cache_scope, cache_key_hash, hit, eligibility_reason,
			invalidation_reason, ttl_seconds, source_freshness, classification, actor,
			repository, workflow, estimated_savings_usd, avoided_tokens, avoided_calls, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, c.ID, c.SessionID, c.CacheScope, c.CacheKeyHash, boolToInt(c.Hit), c.EligibilityReason,
		c.InvalidationReason, c.TTLSeconds, c.SourceFreshness, c.Classification, c.Actor,
		c.Repository, c.Workflow, c.EstimatedSavingsUSD, c.AvoidedTokens, c.AvoidedCalls,
		c.RecordedAt.Format(time.RFC3339Nano))
	return err
}

func (s *DurableRegistryStore) CacheOutcomes() ([]CacheOutcome, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, cache_scope, cache_key_hash, hit, eligibility_reason,
			invalidation_reason, ttl_seconds, source_freshness, classification, actor,
			repository, workflow, estimated_savings_usd, avoided_tokens, avoided_calls, recorded_at
		FROM cache_outcomes ORDER BY recorded_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCacheOutcomes(rows)
}

func scanCacheOutcomes(rows *sql.Rows) ([]CacheOutcome, error) {
	var out []CacheOutcome
	for rows.Next() {
		var c CacheOutcome
		var recordedAtStr string
		var hit int
		if err := rows.Scan(&c.ID, &c.SessionID, &c.CacheScope, &c.CacheKeyHash, &hit, &c.EligibilityReason,
			&c.InvalidationReason, &c.TTLSeconds, &c.SourceFreshness, &c.Classification, &c.Actor,
			&c.Repository, &c.Workflow, &c.EstimatedSavingsUSD, &c.AvoidedTokens, &c.AvoidedCalls,
			&recordedAtStr); err != nil {
			return nil, err
		}
		c.Hit = hit != 0
		c.RecordedAt, _ = time.Parse(time.RFC3339Nano, recordedAtStr)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Evidence methods.

func (s *DurableRegistryStore) AppendEvidence(e EvidenceRecord) error {
	if e.RecordedAt.IsZero() {
		e.RecordedAt = s.now().UTC()
	}
	_, err := s.db.Exec(`
		INSERT INTO evidence_records (
			id, session_id, evidence_type, description, test_result, quality_system_link,
			security_finding, approval_receipt, patch_decision, external_ticket,
			trust_level, enforcement_mode, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, e.ID, e.SessionID, e.EvidenceType, e.Description, e.TestResult, e.QualitySystemLink,
		e.SecurityFinding, e.ApprovalReceipt, e.PatchDecision, e.ExternalTicket,
		e.TrustLevel, e.EnforcementMode,
		e.RecordedAt.Format(time.RFC3339Nano))
	return err
}

func (s *DurableRegistryStore) Evidence() ([]EvidenceRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, evidence_type, description, test_result, quality_system_link,
			security_finding, approval_receipt, patch_decision, external_ticket,
			trust_level, enforcement_mode, recorded_at
		FROM evidence_records ORDER BY recorded_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvidence(rows)
}

func scanEvidence(rows *sql.Rows) ([]EvidenceRecord, error) {
	var out []EvidenceRecord
	for rows.Next() {
		var e EvidenceRecord
		var recordedAtStr string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.EvidenceType, &e.Description, &e.TestResult,
			&e.QualitySystemLink, &e.SecurityFinding, &e.ApprovalReceipt, &e.PatchDecision,
			&e.ExternalTicket, &e.TrustLevel, &e.EnforcementMode, &recordedAtStr); err != nil {
			return nil, err
		}
		e.RecordedAt, _ = time.Parse(time.RFC3339Nano, recordedAtStr)
		out = append(out, e)
	}
	return out, rows.Err()
}

// MaturityExport methods.

func (s *DurableRegistryStore) AppendExport(r MaturityExportRecord) error {
	if r.RecordedAt.IsZero() {
		r.RecordedAt = s.now().UTC()
	}
	evidenceLinksJSON, _ := json.Marshal(r.EvidenceLinks)
	_, err := s.db.Exec(`
		INSERT INTO maturity_exports (
			session_id, event_type, actor, team, use_case_id, workflow_id, work_item_id,
			repository, branch, commit_hash, classification, risk_level, agent, specialist,
			runtime, model_alias, model_resolved, final_status, policy_decision, policy_reason,
			secret_scan_result, kill_switch_result, oauth_failure_result, tool_loop_cap_result,
			cost_cap_result, human_gate_decision, baseline_cost_usd, model_cost_usd, tool_cost_usd,
			platform_cost_usd, review_cost_usd, verification_cost_usd, retry_count, context_manifest_id,
			evidence_links_json, patch_decision, quality_result, evidence_completeness, cycle_time_seconds,
			blocked_event_category, cache_hits, cache_misses, cache_savings_estimate, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.SessionID, r.EventType, r.Actor, r.Team, r.UseCaseID, r.WorkflowID, r.WorkItemID,
		r.Repository, r.Branch, r.Commit, r.Classification, r.RiskLevel, r.Agent, r.Specialist,
		r.Runtime, r.ModelAlias, r.ModelResolved, r.FinalStatus, r.PolicyDecision, r.PolicyReason,
		r.SecretScanResult, r.KillSwitchResult, r.OAuthFailureResult, r.ToolLoopCapResult,
		r.CostCapResult, r.HumanGateDecision, r.BaselineCostUSD, r.ModelCostUSD, r.ToolCostUSD,
		r.PlatformCostUSD, r.ReviewCostUSD, r.VerificationCostUSD, r.RetryCount, r.ContextManifestID,
		string(evidenceLinksJSON), r.PatchDecision, r.QualityResult, r.EvidenceCompleteness, r.CycleTimeSeconds,
		r.BlockedEventCategory, r.CacheHits, r.CacheMisses, r.CacheSavingsEstimate, r.RecordedAt.Format(time.RFC3339Nano))
	return err
}

func (s *DurableRegistryStore) Exports() ([]MaturityExportRecord, error) {
	rows, err := s.db.Query(`
		SELECT session_id, event_type, actor, team, use_case_id, workflow_id, work_item_id,
			repository, branch, commit_hash, classification, risk_level, agent, specialist,
			runtime, model_alias, model_resolved, final_status, policy_decision, policy_reason,
			secret_scan_result, kill_switch_result, oauth_failure_result, tool_loop_cap_result,
			cost_cap_result, human_gate_decision, baseline_cost_usd, model_cost_usd, tool_cost_usd,
			platform_cost_usd, review_cost_usd, verification_cost_usd, retry_count, context_manifest_id,
			evidence_links_json, patch_decision, quality_result, evidence_completeness, cycle_time_seconds,
			blocked_event_category, cache_hits, cache_misses, cache_savings_estimate, recorded_at
		FROM maturity_exports ORDER BY recorded_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMaturityExports(rows)
}

func scanMaturityExports(rows *sql.Rows) ([]MaturityExportRecord, error) {
	var out []MaturityExportRecord
	for rows.Next() {
		var r MaturityExportRecord
		var evidenceLinksJSON string
		var recordedAtStr string
		if err := rows.Scan(&r.SessionID, &r.EventType, &r.Actor, &r.Team, &r.UseCaseID, &r.WorkflowID, &r.WorkItemID,
			&r.Repository, &r.Branch, &r.Commit, &r.Classification, &r.RiskLevel, &r.Agent, &r.Specialist,
			&r.Runtime, &r.ModelAlias, &r.ModelResolved, &r.FinalStatus, &r.PolicyDecision, &r.PolicyReason,
			&r.SecretScanResult, &r.KillSwitchResult, &r.OAuthFailureResult, &r.ToolLoopCapResult,
			&r.CostCapResult, &r.HumanGateDecision, &r.BaselineCostUSD, &r.ModelCostUSD, &r.ToolCostUSD,
			&r.PlatformCostUSD, &r.ReviewCostUSD, &r.VerificationCostUSD, &r.RetryCount, &r.ContextManifestID,
			&evidenceLinksJSON, &r.PatchDecision, &r.QualityResult, &r.EvidenceCompleteness, &r.CycleTimeSeconds,
			&r.BlockedEventCategory, &r.CacheHits, &r.CacheMisses, &r.CacheSavingsEstimate, &recordedAtStr); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(evidenceLinksJSON), &r.EvidenceLinks)
		r.RecordedAt, _ = time.Parse(time.RFC3339Nano, recordedAtStr)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Close closes the underlying database connection.
func (s *DurableRegistryStore) Close() error {
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
