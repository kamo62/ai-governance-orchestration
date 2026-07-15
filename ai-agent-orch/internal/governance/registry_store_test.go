package governance

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableRegistryStore_UseCase(t *testing.T) {
	s, err := NewDurableRegistryStore(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	uc := UseCase{
		ID:              "uc_test",
		Owner:           "local-dev",
		Domain:          "engineering",
		ExpectedBenefit: "faster tests",
		Classification:  "internal",
		RiskLevel:       "medium",
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.RegisterUseCase(uc); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, ok := s.GetUseCase("uc_test")
	if !ok {
		t.Fatal("expected use case to exist")
	}
	if got.Owner != "local-dev" {
		t.Fatalf("unexpected owner: %s", got.Owner)
	}

	_, ok = s.GetUseCase("missing")
	if ok {
		t.Fatal("expected missing use case to not exist")
	}
}

func TestDurableRegistryStore_Workflow(t *testing.T) {
	s, err := NewDurableRegistryStore(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	wf := Workflow{
		ID:          "wf_1",
		Name:        "DevSecOps Review",
		Description: "Security review workflow",
		Stages:      []string{"scan", "review"},
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.RegisterWorkflow(wf); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, ok := s.GetWorkflow("wf_1")
	if !ok {
		t.Fatal("expected workflow to exist")
	}
	if got.Name != "DevSecOps Review" {
		t.Fatalf("unexpected name: %s", got.Name)
	}
	if len(got.Stages) != 2 {
		t.Fatalf("unexpected stages: %v", got.Stages)
	}
}

func TestDurableRegistryStore_ContextManifest(t *testing.T) {
	s, err := NewDurableRegistryStore(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	m := ContextManifest{
		ID:              "ctx_1",
		SessionID:       "sess_a",
		Summary:         "test summary",
		SourceSystem:    "jira",
		SourceObjectID:  "JIRA-123",
		Actor:           "local-dev",
		AuthScope:       "read",
		FetchedAt:       time.Now().UTC(),
		Classification:  "internal",
		CacheStatus:     "miss",
		ChunkHashes:     []string{"sha256:abc"},
		InfluencedModel: true,
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.CreateManifest(m); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, ok := s.GetManifest("ctx_1")
	if !ok {
		t.Fatal("expected manifest to exist")
	}
	if got.SessionID != "sess_a" {
		t.Fatalf("unexpected session: %s", got.SessionID)
	}
	if !got.InfluencedModel {
		t.Fatal("expected influenced_model")
	}
	if len(got.ChunkHashes) != 1 {
		t.Fatalf("unexpected chunk hashes: %v", got.ChunkHashes)
	}
}

func TestDurableRegistryStore_CacheOutcome(t *testing.T) {
	s, err := NewDurableRegistryStore(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	c := CacheOutcome{
		ID:           "co_1",
		SessionID:    "sess_1",
		CacheScope:   "session",
		CacheKeyHash: "sha256:abc",
		Hit:          true,
		RecordedAt:   time.Now().UTC(),
	}
	if err := s.AppendCacheOutcome(c); err != nil {
		t.Fatalf("append: %v", err)
	}

	outcomes, err := s.CacheOutcomes()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(outcomes))
	}
	if !outcomes[0].Hit {
		t.Fatal("expected hit")
	}
}

func TestDurableRegistryStore_Evidence(t *testing.T) {
	s, err := NewDurableRegistryStore(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	confidence := 0.7
	e := EvidenceRecord{
		ID:           "ev_1",
		SessionID:    "sess_1",
		EvidenceType: "test_result",
		Description:  "unit tests passed",
		TestResult:   "pass",
		Confidence:   &confidence,
		RecordedAt:   time.Now().UTC(),
	}
	if _, _, err := s.AppendEvidence(e); err != nil {
		t.Fatalf("append: %v", err)
	}

	ev, err := s.Evidence()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ev) != 1 {
		t.Fatalf("expected 1 evidence, got %d", len(ev))
	}
	if ev[0].EvidenceType != "test_result" {
		t.Fatalf("unexpected type: %s", ev[0].EvidenceType)
	}
	if ev[0].Confidence == nil || *ev[0].Confidence != 0.7 || ev[0].ConfidenceBand != "medium" {
		t.Fatalf("unexpected confidence response: %#v", ev[0])
	}
}

// TestDurableRegistryStore_AppendEvidenceDedupesClientEventID verifies the
// unique (session_id, client_event_id) index makes a retried evidence post
// idempotent: the second insert is caught by the index rather than a
// separate lookup, and the original record is returned with duplicate=true.
func TestDurableRegistryStore_AppendEvidenceDedupesClientEventID(t *testing.T) {
	s, err := NewDurableRegistryStore(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	first, duplicate, err := s.AppendEvidence(EvidenceRecord{
		ID:            "ev_1",
		SessionID:     "sess_1",
		EvidenceType:  "test_result",
		Description:   "first attempt",
		ClientEventID: "cev_abc",
		RecordedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("append first: %v", err)
	}
	if duplicate {
		t.Fatal("expected the first append to not be a duplicate")
	}

	retry, duplicate, err := s.AppendEvidence(EvidenceRecord{
		ID:            "ev_2",
		SessionID:     "sess_1",
		EvidenceType:  "test_result",
		Description:   "retried attempt",
		ClientEventID: "cev_abc",
		RecordedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("append retry: %v", err)
	}
	if !duplicate {
		t.Fatal("expected the retried append to be reported as a duplicate")
	}
	if retry.ID != first.ID {
		t.Fatalf("expected the duplicate to return the original record %q, got %q", first.ID, retry.ID)
	}

	// A different session may reuse the same client_event_id.
	if _, duplicate, err := s.AppendEvidence(EvidenceRecord{
		ID:            "ev_3",
		SessionID:     "sess_2",
		EvidenceType:  "test_result",
		Description:   "different session",
		ClientEventID: "cev_abc",
		RecordedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append other session: %v", err)
	} else if duplicate {
		t.Fatal("expected a different session to not collide on the same client_event_id")
	}

	all, err := s.Evidence()
	if err != nil {
		t.Fatalf("list evidence: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected exactly 2 stored records (no duplicate persisted), got %d", len(all))
	}

	// Evidence without a client_event_id is untouched by the partial index:
	// two separate empty-ID records must coexist.
	if _, duplicate, err := s.AppendEvidence(EvidenceRecord{
		ID:           "ev_4",
		SessionID:    "sess_1",
		EvidenceType: "test_result",
		Description:  "no client event id, first",
		RecordedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append no-client-event-id first: %v", err)
	} else if duplicate {
		t.Fatal("expected empty client_event_id to never be treated as a duplicate")
	}
	if _, duplicate, err := s.AppendEvidence(EvidenceRecord{
		ID:           "ev_5",
		SessionID:    "sess_1",
		EvidenceType: "test_result",
		Description:  "no client event id, second",
		RecordedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append no-client-event-id second: %v", err)
	} else if duplicate {
		t.Fatal("expected empty client_event_id to never be treated as a duplicate")
	}
}

func TestDurableRegistryStore_MigratesExistingEvidenceTrustColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "registry.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE evidence_records (
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
			recorded_at TEXT NOT NULL
		);
	`)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close seed db: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("seed legacy evidence table: %v", err)
	}

	s, err := NewDurableRegistryStore(dbPath)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer s.Close()

	if _, _, err := s.AppendEvidence(EvidenceRecord{
		ID:              "ev_migrated",
		SessionID:       "sess_1",
		EvidenceType:    "external_tool_call",
		Description:     "reported native tool call",
		TrustLevel:      "self_reported",
		EnforcementMode: "advisory",
		RecordedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append migrated evidence: %v", err)
	}
	ev, err := s.Evidence()
	if err != nil {
		t.Fatalf("list migrated evidence: %v", err)
	}
	if len(ev) != 1 {
		t.Fatalf("expected one evidence record, got %d", len(ev))
	}
	if ev[0].TrustLevel != "self_reported" || ev[0].EnforcementMode != "advisory" {
		t.Fatalf("expected migrated trust/enforcement fields, got %#v", ev[0])
	}
}

func TestDurableRegistryStore_MigratesPrePhase2EvidenceProvenance(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "registry.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE evidence_records (
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
		INSERT INTO evidence_records (id, session_id, evidence_type, description, trust_level, enforcement_mode, recorded_at) VALUES
			('gateway', 'sess_1', 'test', 'gateway', 'gateway_enforced', 'gateway', '2026-07-10T00:00:00Z'),
			('managed', 'sess_1', 'test', 'managed', 'managed_client', 'managed', '2026-07-10T00:00:01Z'),
			('self', 'sess_1', 'test', 'self', 'self_reported', 'advisory', '2026-07-10T00:00:02Z'),
			('unknown', 'sess_1', 'test', 'unknown', 'unknown', 'advisory', '2026-07-10T00:00:03Z');
	`)
	if err != nil {
		t.Fatalf("seed pre-phase-2 evidence: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	for open := 0; open < 2; open++ {
		store, err := NewDurableRegistryStore(dbPath)
		if err != nil {
			t.Fatalf("open migrated store: %v", err)
		}
		evidence, err := store.Evidence()
		if err != nil {
			t.Fatalf("list migrated evidence: %v", err)
		}
		wantAuthority := map[string]int{"gateway": 1, "managed": 3, "self": 4, "unknown": 4}
		if len(evidence) != len(wantAuthority) {
			t.Fatalf("expected %d rows, got %d", len(wantAuthority), len(evidence))
		}
		for _, record := range evidence {
			if record.SourceAuthority != wantAuthority[record.ID] || record.Status != "legacy" || record.EvidenceStrength != "weak" || record.SubjectKey != "" || record.Confidence != nil {
				t.Fatalf("unexpected migrated provenance for %s: %#v", record.ID, record)
			}
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close migrated store: %v", err)
		}
	}
}

func TestDurableRegistryStore_RecordsImmutableEvidenceDecisions(t *testing.T) {
	store, err := NewDurableRegistryStore(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, _, err := store.AppendEvidence(EvidenceRecord{ID: "ev_1", SessionID: "sess_1", EvidenceType: "test", Description: "result", Status: "candidate", SourceAuthority: 3}); err != nil {
		t.Fatalf("append evidence: %v", err)
	}
	for i, status := range []string{"confirmed", "rejected"} {
		decision := &EvidenceDecision{
			ID:          status,
			EvidenceID:  "ev_1",
			ConfirmedBy: "admin-operator",
			Decision:    status,
			Reason:      status + " reason",
			RecordedAt:  time.Date(2026, 7, 10, 0, 0, i, 0, time.UTC),
		}
		got, err := store.TransitionEvidence("ev_1", status, decision)
		if err != nil {
			t.Fatalf("transition to %s: %v", status, err)
		}
		if got.Status != status || got.SourceAuthority != 2 {
			t.Fatalf("unexpected transitioned evidence: %#v", got)
		}
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM evidence_confirmations WHERE evidence_id = 'ev_1'`).Scan(&count); err != nil {
		t.Fatalf("count immutable decisions: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected two immutable decisions, got %d", count)
	}
}

func TestDurableRegistryStore_MaturityExport(t *testing.T) {
	s, err := NewDurableRegistryStore(t.TempDir() + "/registry.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	r := MaturityExportRecord{
		SessionID:       "sess_1",
		EventType:       "session.summary",
		Actor:           "local-dev",
		UseCaseID:       "uc_1",
		PolicyDecision:  "allowed",
		BaselineCostUSD: 1000,
		ModelCostUSD:    5,
		RecordedAt:      time.Now().UTC(),
	}
	if err := s.AppendExport(r); err != nil {
		t.Fatalf("append: %v", err)
	}

	exports, err := s.Exports()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(exports) != 1 {
		t.Fatalf("expected 1 export, got %d", len(exports))
	}
	if exports[0].SessionID != "sess_1" {
		t.Fatalf("unexpected session: %s", exports[0].SessionID)
	}
	if exports[0].BaselineCostUSD != 1000 {
		t.Fatalf("unexpected baseline cost: %f", exports[0].BaselineCostUSD)
	}
}
