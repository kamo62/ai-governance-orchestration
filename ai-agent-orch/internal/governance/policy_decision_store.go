package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/policyengine"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
)

// PolicyDecisionRecord is the durable projection of a policy-engine decision.
// It deliberately lives outside the append-only audit ledger.
type PolicyDecisionRecord struct {
	DecisionID       string
	SessionID        string
	CorrelationID    string
	RecordedAt       time.Time
	Engine           string
	Allowed          bool
	RequiresApproval bool
	Reason           string
	Findings         []string
	ActionType       string
	Resource         string
	ToolName         string
	Classification   string
	CostCapUSD       float64
	EstimatedCostUSD float64
	Actor            string
	Agent            string
}

// PolicyDecisionStore records evaluated policy decisions. Decisions are
// immutable: re-recording the same decision ID is an idempotent no-op.
type PolicyDecisionStore interface {
	RecordPolicyDecision(context.Context, PolicyDecisionRecord) error
	GetPolicyDecision(context.Context, string) (PolicyDecisionRecord, bool, error)
	ListPolicyDecisions(context.Context, string) ([]PolicyDecisionRecord, error)
}

// ManagedClientReceiptStore atomically reserves client-event IDs before their
// corresponding audit event is appended.
type ManagedClientReceiptStore interface {
	ReserveManagedClientReceipt(context.Context, ManagedClientReceipt) (bool, error)
	GetManagedClientReceipt(context.Context, string, string) (ManagedClientReceipt, bool, error)
	ReleaseManagedClientReceipt(context.Context, ManagedClientReceipt) error
}

type ManagedClientReceipt struct {
	SessionID     string
	ClientEventID string
	AuditEventID  string
	RecordedAt    time.Time
}

// SQLitePolicyDecisionStore keeps governance projections in the same SQLite
// file as other governance stores without changing the audit ledger schema.
type SQLitePolicyDecisionStore struct {
	db *sql.DB
}

func NewSQLitePolicyDecisionStore(dbPath string) (*SQLitePolicyDecisionStore, error) {
	if dbPath == "" {
		return nil, errors.New("sqlite policy decision db path is required")
	}
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, fmt.Errorf("create policy decision db directory: %w", err)
		}
	}
	db, err := sqlitex.Open(dbPath, "policy decision")
	if err != nil {
		return nil, err
	}
	store := &SQLitePolicyDecisionStore{db: db}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate policy decisions: %w", err)
	}
	if dbPath != ":memory:" {
		if err := os.Chmod(dbPath, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set policy decision db permissions: %w", err)
		}
	}
	return store, nil
}

func (s *SQLitePolicyDecisionStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS policy_decisions (
			decision_id TEXT PRIMARY KEY,
			session_id TEXT NULL,
			correlation_id TEXT NULL,
			recorded_at TEXT NOT NULL,
			engine TEXT NOT NULL,
			allowed INTEGER NOT NULL,
			requires_approval INTEGER NOT NULL,
			reason TEXT NOT NULL,
			findings TEXT NOT NULL,
			action_type TEXT NOT NULL,
			resource TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			classification TEXT NOT NULL,
			cost_cap_usd REAL NOT NULL,
			estimated_cost_usd REAL NOT NULL,
			actor TEXT NOT NULL,
			agent TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS managed_client_receipts (
			session_id TEXT NOT NULL,
			client_event_id TEXT NOT NULL,
			audit_event_id TEXT NOT NULL,
			recorded_at TEXT NOT NULL,
			PRIMARY KEY (session_id, client_event_id)
		);
		CREATE INDEX IF NOT EXISTS idx_policy_decisions_session ON policy_decisions(session_id);
	`)
	if err != nil {
		return err
	}
	for _, column := range []struct {
		table, name, definition string
	}{
		{"policy_decisions", "session_id", "TEXT NULL"},
		{"policy_decisions", "correlation_id", "TEXT NULL"},
		{"policy_decisions", "recorded_at", "TEXT NOT NULL DEFAULT ''"},
		{"policy_decisions", "engine", "TEXT NOT NULL DEFAULT ''"},
		{"policy_decisions", "allowed", "INTEGER NOT NULL DEFAULT 0"},
		{"policy_decisions", "requires_approval", "INTEGER NOT NULL DEFAULT 0"},
		{"policy_decisions", "reason", "TEXT NOT NULL DEFAULT ''"},
		{"policy_decisions", "findings", "TEXT NOT NULL DEFAULT '[]'"},
		{"policy_decisions", "action_type", "TEXT NOT NULL DEFAULT ''"},
		{"policy_decisions", "resource", "TEXT NOT NULL DEFAULT ''"},
		{"policy_decisions", "tool_name", "TEXT NOT NULL DEFAULT ''"},
		{"policy_decisions", "classification", "TEXT NOT NULL DEFAULT ''"},
		{"policy_decisions", "cost_cap_usd", "REAL NOT NULL DEFAULT 0"},
		{"policy_decisions", "estimated_cost_usd", "REAL NOT NULL DEFAULT 0"},
		{"policy_decisions", "actor", "TEXT NOT NULL DEFAULT ''"},
		{"policy_decisions", "agent", "TEXT NOT NULL DEFAULT ''"},
		{"managed_client_receipts", "audit_event_id", "TEXT NOT NULL DEFAULT ''"},
		{"managed_client_receipts", "recorded_at", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLitePolicyDecisionStore) ensureColumn(table, column, definition string) error {
	columns, err := sqlitex.TableColumns(s.db, table)
	if err != nil {
		return fmt.Errorf("inspect %s schema: %w", table, err)
	}
	if columns[column] {
		return nil
	}
	if _, err := s.db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition)); err != nil {
		// Another process may win the schema probe -> ALTER TABLE race.
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return nil
		}
		return fmt.Errorf("add %s column %s: %w", table, column, err)
	}
	return nil
}

func (s *SQLitePolicyDecisionStore) RecordPolicyDecision(ctx context.Context, record PolicyDecisionRecord) error {
	if s == nil || s.db == nil {
		return errors.New("policy decision store unavailable")
	}
	if strings.TrimSpace(record.DecisionID) == "" {
		return errors.New("policy decision ID is required")
	}
	if record.RecordedAt.IsZero() {
		record.RecordedAt = time.Now().UTC()
	}
	findings := record.Findings
	if findings == nil {
		findings = []string{}
	}
	findingsJSON, err := json.Marshal(findings)
	if err != nil {
		return fmt.Errorf("encode policy findings: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO policy_decisions (
			decision_id, session_id, correlation_id, recorded_at, engine, allowed, requires_approval,
			reason, findings, action_type, resource, tool_name, classification, cost_cap_usd,
			estimated_cost_usd, actor, agent
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(decision_id) DO NOTHING
	`, record.DecisionID, nullableString(record.SessionID), nullableString(record.CorrelationID),
		record.RecordedAt.UTC().Format(time.RFC3339Nano), record.Engine, boolToInt(record.Allowed), boolToInt(record.RequiresApproval),
		record.Reason, string(findingsJSON), record.ActionType, record.Resource, record.ToolName, record.Classification,
		record.CostCapUSD, record.EstimatedCostUSD, record.Actor, record.Agent)
	if err != nil {
		return fmt.Errorf("record policy decision: %w", err)
	}
	return nil
}

func (s *SQLitePolicyDecisionStore) GetPolicyDecision(ctx context.Context, decisionID string) (PolicyDecisionRecord, bool, error) {
	if s == nil || s.db == nil {
		return PolicyDecisionRecord{}, false, errors.New("policy decision store unavailable")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT decision_id, session_id, correlation_id, recorded_at, engine, allowed, requires_approval,
			reason, findings, action_type, resource, tool_name, classification, cost_cap_usd,
			estimated_cost_usd, actor, agent
		FROM policy_decisions WHERE decision_id = ?
	`, decisionID)
	record, err := scanPolicyDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PolicyDecisionRecord{}, false, nil
	}
	if err != nil {
		return PolicyDecisionRecord{}, false, err
	}
	return record, true, nil
}

func (s *SQLitePolicyDecisionStore) ListPolicyDecisions(ctx context.Context, sessionID string) ([]PolicyDecisionRecord, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("policy decision store unavailable")
	}
	query := `
		SELECT decision_id, session_id, correlation_id, recorded_at, engine, allowed, requires_approval,
			reason, findings, action_type, resource, tool_name, classification, cost_cap_usd,
			estimated_cost_usd, actor, agent
		FROM policy_decisions`
	args := []any{}
	if strings.TrimSpace(sessionID) != "" {
		query += ` WHERE session_id = ?`
		args = append(args, sessionID)
	}
	query += ` ORDER BY recorded_at, decision_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list policy decisions: %w", err)
	}
	defer rows.Close()
	var records []PolicyDecisionRecord
	for rows.Next() {
		record, err := scanPolicyDecision(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func scanPolicyDecision(scanner interface{ Scan(...any) error }) (PolicyDecisionRecord, error) {
	var record PolicyDecisionRecord
	var sessionID, correlationID sql.NullString
	var recordedAt, findingsJSON string
	var allowed, requiresApproval int
	if err := scanner.Scan(
		&record.DecisionID, &sessionID, &correlationID, &recordedAt, &record.Engine, &allowed, &requiresApproval,
		&record.Reason, &findingsJSON, &record.ActionType, &record.Resource, &record.ToolName, &record.Classification,
		&record.CostCapUSD, &record.EstimatedCostUSD, &record.Actor, &record.Agent,
	); err != nil {
		return PolicyDecisionRecord{}, err
	}
	record.SessionID = sessionID.String
	record.CorrelationID = correlationID.String
	record.Allowed = allowed != 0
	record.RequiresApproval = requiresApproval != 0
	record.RecordedAt, _ = time.Parse(time.RFC3339Nano, recordedAt)
	if err := json.Unmarshal([]byte(findingsJSON), &record.Findings); err != nil {
		return PolicyDecisionRecord{}, fmt.Errorf("decode policy findings: %w", err)
	}
	return record, nil
}

func (s *SQLitePolicyDecisionStore) ReserveManagedClientReceipt(ctx context.Context, receipt ManagedClientReceipt) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("policy decision store unavailable")
	}
	if strings.TrimSpace(receipt.SessionID) == "" || strings.TrimSpace(receipt.ClientEventID) == "" || strings.TrimSpace(receipt.AuditEventID) == "" {
		return false, errors.New("managed client receipt requires session, client event, and audit event IDs")
	}
	if receipt.RecordedAt.IsZero() {
		receipt.RecordedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO managed_client_receipts (session_id, client_event_id, audit_event_id, recorded_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(session_id, client_event_id) DO NOTHING
	`, receipt.SessionID, receipt.ClientEventID, receipt.AuditEventID, receipt.RecordedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("reserve managed client receipt: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check managed client receipt reservation: %w", err)
	}
	return inserted == 1, nil
}

func (s *SQLitePolicyDecisionStore) GetManagedClientReceipt(ctx context.Context, sessionID, clientEventID string) (ManagedClientReceipt, bool, error) {
	if s == nil || s.db == nil {
		return ManagedClientReceipt{}, false, errors.New("policy decision store unavailable")
	}
	var receipt ManagedClientReceipt
	var recordedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, client_event_id, audit_event_id, recorded_at
		FROM managed_client_receipts WHERE session_id = ? AND client_event_id = ?
	`, sessionID, clientEventID).Scan(&receipt.SessionID, &receipt.ClientEventID, &receipt.AuditEventID, &recordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedClientReceipt{}, false, nil
	}
	if err != nil {
		return ManagedClientReceipt{}, false, fmt.Errorf("get managed client receipt: %w", err)
	}
	receipt.RecordedAt, _ = time.Parse(time.RFC3339Nano, recordedAt)
	return receipt, true, nil
}

func (s *SQLitePolicyDecisionStore) ReleaseManagedClientReceipt(ctx context.Context, receipt ManagedClientReceipt) error {
	if s == nil || s.db == nil {
		return errors.New("policy decision store unavailable")
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM managed_client_receipts
		WHERE session_id = ? AND client_event_id = ? AND audit_event_id = ?
	`, receipt.SessionID, receipt.ClientEventID, receipt.AuditEventID)
	if err != nil {
		return fmt.Errorf("release managed client receipt: %w", err)
	}
	return nil
}

func (s *SQLitePolicyDecisionStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// MemoryPolicyDecisionStore is the non-durable wiring and unit-test fallback.
type MemoryPolicyDecisionStore struct {
	mu      sync.RWMutex
	records map[string]PolicyDecisionRecord
}

func NewMemoryPolicyDecisionStore() *MemoryPolicyDecisionStore {
	return &MemoryPolicyDecisionStore{records: make(map[string]PolicyDecisionRecord)}
}

func (s *MemoryPolicyDecisionStore) RecordPolicyDecision(_ context.Context, record PolicyDecisionRecord) error {
	if s == nil {
		return errors.New("policy decision store unavailable")
	}
	if strings.TrimSpace(record.DecisionID) == "" {
		return errors.New("policy decision ID is required")
	}
	if record.RecordedAt.IsZero() {
		record.RecordedAt = time.Now().UTC()
	}
	record.Findings = append([]string(nil), record.Findings...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[record.DecisionID]; !exists {
		s.records[record.DecisionID] = record
	}
	return nil
}

func (s *MemoryPolicyDecisionStore) GetPolicyDecision(_ context.Context, decisionID string) (PolicyDecisionRecord, bool, error) {
	if s == nil {
		return PolicyDecisionRecord{}, false, errors.New("policy decision store unavailable")
	}
	s.mu.RLock()
	record, ok := s.records[decisionID]
	s.mu.RUnlock()
	record.Findings = append([]string(nil), record.Findings...)
	return record, ok, nil
}

func (s *MemoryPolicyDecisionStore) ListPolicyDecisions(_ context.Context, sessionID string) ([]PolicyDecisionRecord, error) {
	if s == nil {
		return nil, errors.New("policy decision store unavailable")
	}
	s.mu.RLock()
	records := make([]PolicyDecisionRecord, 0, len(s.records))
	for _, record := range s.records {
		if sessionID == "" || record.SessionID == sessionID {
			record.Findings = append([]string(nil), record.Findings...)
			records = append(records, record)
		}
	}
	s.mu.RUnlock()
	sort.Slice(records, func(i, j int) bool {
		if records[i].RecordedAt.Equal(records[j].RecordedAt) {
			return records[i].DecisionID < records[j].DecisionID
		}
		return records[i].RecordedAt.Before(records[j].RecordedAt)
	})
	return records, nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func policyDecisionRecordFromEvaluation(request policyengine.Request, decision policyengine.Decision, actor, sessionID, correlationID string) PolicyDecisionRecord {
	costCapUSD := 0.0
	if request.CostCapEnabled {
		costCapUSD = request.SessionCostCapUSD
	}
	return PolicyDecisionRecord{
		DecisionID:       decision.DecisionID,
		SessionID:        sessionID,
		CorrelationID:    correlationID,
		RecordedAt:       time.Now().UTC(),
		Engine:           defaultString(decision.Engine, "native"),
		Allowed:          decision.Allowed,
		RequiresApproval: decision.RequiresApproval,
		Reason:           decision.Reason,
		Findings:         append([]string(nil), decision.Findings...),
		ActionType:       request.ActionType,
		Resource:         request.Resource,
		ToolName:         request.ToolName,
		Classification:   request.Classification,
		CostCapUSD:       costCapUSD,
		EstimatedCostUSD: request.EstimatedCostUSD,
		Actor:            actor,
		Agent:            request.AgentName,
	}
}
