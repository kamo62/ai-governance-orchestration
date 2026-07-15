package governance

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLitePolicyDecisionStoreMigratesLegacyDatabaseAndPersistsRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "governance.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (session_id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("seed legacy database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := NewSQLitePolicyDecisionStore(path)
	if err != nil {
		t.Fatalf("open migrated policy decision store: %v", err)
	}
	defer store.Close()

	record := PolicyDecisionRecord{
		DecisionID:       "pol_test_1",
		SessionID:        "sess_1",
		RecordedAt:       time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
		Engine:           "native",
		Allowed:          true,
		RequiresApproval: true,
		Reason:           "allowed",
		Findings:         []string{"finding_1"},
		ActionType:       "session.create",
		Classification:   "internal",
		CostCapUSD:       2.5,
		EstimatedCostUSD: 1.25,
		Actor:            "dev@example.test",
		Agent:            "unit-tests",
	}
	if err := store.RecordPolicyDecision(context.Background(), record); err != nil {
		t.Fatalf("record policy decision: %v", err)
	}
	if err := store.RecordPolicyDecision(context.Background(), record); err != nil {
		t.Fatalf("idempotent record policy decision: %v", err)
	}

	got, found, err := store.GetPolicyDecision(context.Background(), record.DecisionID)
	if err != nil || !found {
		t.Fatalf("get policy decision: found=%v err=%v", found, err)
	}
	if got.SessionID != record.SessionID || !got.Allowed || !got.RequiresApproval || len(got.Findings) != 1 || got.Findings[0] != "finding_1" {
		t.Fatalf("unexpected policy decision: %#v", got)
	}
	listed, err := store.ListPolicyDecisions(context.Background(), "sess_1")
	if err != nil {
		t.Fatalf("list policy decisions: %v", err)
	}
	if len(listed) != 1 || listed[0].DecisionID != record.DecisionID {
		t.Fatalf("unexpected listed decisions: %#v", listed)
	}

	receipt := ManagedClientReceipt{SessionID: "sess_1", ClientEventID: "client_evt_1", AuditEventID: "evt_server_1", RecordedAt: record.RecordedAt}
	reserved, err := store.ReserveManagedClientReceipt(context.Background(), receipt)
	if err != nil || !reserved {
		t.Fatalf("reserve receipt: reserved=%v err=%v", reserved, err)
	}
	reserved, err = store.ReserveManagedClientReceipt(context.Background(), receipt)
	if err != nil || reserved {
		t.Fatalf("duplicate receipt reservation: reserved=%v err=%v", reserved, err)
	}
	if err := store.ReleaseManagedClientReceipt(context.Background(), receipt); err != nil {
		t.Fatalf("release receipt: %v", err)
	}
	reserved, err = store.ReserveManagedClientReceipt(context.Background(), receipt)
	if err != nil || !reserved {
		t.Fatalf("reserve receipt after release: reserved=%v err=%v", reserved, err)
	}
}

func TestMemoryPolicyDecisionStoreRecordsImmutableCopy(t *testing.T) {
	store := NewMemoryPolicyDecisionStore()
	record := PolicyDecisionRecord{DecisionID: "pol_memory", Findings: []string{"original"}}
	if err := store.RecordPolicyDecision(context.Background(), record); err != nil {
		t.Fatalf("record: %v", err)
	}
	record.Findings[0] = "mutated"
	got, found, err := store.GetPolicyDecision(context.Background(), "pol_memory")
	if err != nil || !found {
		t.Fatalf("get: found=%v err=%v", found, err)
	}
	if got.Findings[0] != "original" {
		t.Fatalf("stored decision was mutated through caller slice: %#v", got)
	}
}
