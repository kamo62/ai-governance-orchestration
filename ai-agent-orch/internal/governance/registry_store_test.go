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

	e := EvidenceRecord{
		ID:           "ev_1",
		SessionID:    "sess_1",
		EvidenceType: "test_result",
		Description:  "unit tests passed",
		TestResult:   "pass",
		RecordedAt:   time.Now().UTC(),
	}
	if err := s.AppendEvidence(e); err != nil {
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

	if err := s.AppendEvidence(EvidenceRecord{
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
