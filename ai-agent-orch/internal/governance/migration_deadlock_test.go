package governance

import (
	"fmt"
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/sqlitex"
)

func TestLegacyGovernanceMigrationsDoNotHoldSchemaRowsDuringDDL(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		run    func(string) error
	}{
		{
			name:   "registry",
			schema: `CREATE TABLE evidence_records (id TEXT PRIMARY KEY, session_id TEXT);`,
			run: func(dsn string) error {
				db, err := sqlitex.Open(dsn, "registry migration test")
				if err != nil {
					return err
				}
				defer db.Close()
				return (&DurableRegistryStore{dbPath: dsn, db: db, now: time.Now}).migrate()
			},
		},
		{
			name:   "policy decisions",
			schema: `CREATE TABLE policy_decisions (decision_id TEXT PRIMARY KEY, session_id TEXT); CREATE TABLE managed_client_receipts (session_id TEXT, client_event_id TEXT, PRIMARY KEY (session_id, client_event_id));`,
			run: func(dsn string) error {
				db, err := sqlitex.Open(dsn, "policy migration test")
				if err != nil {
					return err
				}
				defer db.Close()
				return (&SQLitePolicyDecisionStore{db: db}).migrate()
			},
		},
		{
			name:   "sessions",
			schema: `CREATE TABLE sessions (session_id TEXT PRIMARY KEY, actor_subject TEXT NOT NULL, agent TEXT NOT NULL, classification TEXT NOT NULL, prompt_sha256 TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL);`,
			run: func(dsn string) error {
				db, err := sqlitex.Open(dsn, "session migration test")
				if err != nil {
					return err
				}
				defer db.Close()
				return (&SQLiteSessionStore{db: db}).migrate()
			},
		},
		{
			name:   "developer credentials",
			schema: `CREATE TABLE developer_runtime_credentials (id TEXT PRIMARY KEY, actor_subject TEXT, token_sha256 TEXT);`,
			run: func(dsn string) error {
				db, err := sqlitex.Open(dsn, "credential migration test")
				if err != nil {
					return err
				}
				defer db.Close()
				return (&SQLiteDeveloperCredentialStore{db: db}).migrate()
			},
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dsn := fmt.Sprintf("file:governance_migration_%d?mode=memory&cache=shared", i)
			seed, err := sqlitex.Open(dsn, "migration seed")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := seed.Exec(tc.schema); err != nil {
				t.Fatal(err)
			}
			defer seed.Close()
			assertMigrationCompletes(t, func() error { return tc.run(dsn) })
		})
	}
}

func assertMigrationCompletes(t *testing.T, run func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("migration timed out; possible open PRAGMA rows during DDL")
	}
}
