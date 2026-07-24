package governance

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSQLiteSessionStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sessions.db"

	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	rec := SessionRecord{
		SessionID:       "sess_test",
		ParentSessionID: "sess_parent",
		ActorSubject:    "user-1",
		Agent:           "unit-tests",
		Classification:  "internal",
		PromptSHA256:    "abc123",
		Status:          "created",
		CreatedAt:       time.Now().UTC(),
	}

	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.Get(ctx, "sess_test")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ActorSubject != "user-1" {
		t.Fatalf("expected actor user-1, got %s", got.ActorSubject)
	}
	if got.ParentSessionID != "sess_parent" {
		t.Fatalf("expected parent session sess_parent, got %s", got.ParentSessionID)
	}
	if got.Status != "created" {
		t.Fatalf("expected status created, got %s", got.Status)
	}

	if err := store.CompareAndSwapStatus(ctx, "sess_test", "created", "confirmed"); err != nil {
		t.Fatalf("cas: %v", err)
	}

	got2, err := store.Get(ctx, "sess_test")
	if err != nil {
		t.Fatalf("get after cas: %v", err)
	}
	if got2.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %s", got2.Status)
	}

	if err := store.CompareAndSwapStatus(ctx, "sess_test", "created", "running"); err == nil {
		t.Fatal("expected cas to fail with wrong from status")
	}
}

func TestSQLiteSessionStoreListRecentFiltersActorAndOrdersNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	base := time.Date(2026, 6, 8, 7, 0, 0, 0, time.UTC)
	records := []SessionRecord{
		{SessionID: "sess_old", ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "old", Status: "done", CreatedAt: base},
		{SessionID: "sess_other", ActorSubject: "other-dev", Agent: "code-review", Classification: "internal", PromptSHA256: "other", Status: "done", CreatedAt: base.Add(1 * time.Minute)},
		{SessionID: "sess_new", ParentSessionID: "sess_parent", ActorSubject: "local-dev", Agent: "code-review", Classification: "internal", PromptSHA256: "new", Status: "running", CreatedAt: base.Add(2 * time.Minute), RunID: "run_new", PermissionMode: "reviewed", ApprovalMode: "manual"},
	}
	for _, record := range records {
		if err := store.Create(ctx, record); err != nil {
			t.Fatalf("create %s: %v", record.SessionID, err)
		}
	}

	got, err := store.ListRecent(ctx, "local-dev", 10)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two local-dev sessions, got %d: %#v", len(got), got)
	}
	if got[0].SessionID != "sess_new" || got[1].SessionID != "sess_old" {
		t.Fatalf("expected newest local sessions first, got %#v", got)
	}
	if got[0].PromptSHA256 != "new" || got[0].RunID != "run_new" || got[0].ParentSessionID != "sess_parent" {
		t.Fatalf("expected stored fields on newest session, got %#v", got[0])
	}

	limited, err := store.ListRecent(ctx, "local-dev", 1)
	if err != nil {
		t.Fatalf("limited list: %v", err)
	}
	if len(limited) != 1 || limited[0].SessionID != "sess_new" {
		t.Fatalf("expected newest session only, got %#v", limited)
	}
}

func TestSQLiteSessionStoreListRecentSinceFiltersActorAndWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	base := time.Date(2026, 6, 8, 7, 0, 0, 0, time.UTC)
	records := []SessionRecord{
		{SessionID: "sess_too_old", ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "old", Status: "done", CreatedAt: base.Add(-1 * time.Hour)},
		{SessionID: "sess_in_window", ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "in-window", Status: "done", CreatedAt: base.Add(1 * time.Minute)},
		{SessionID: "sess_other_actor", ActorSubject: "other-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "other", Status: "done", CreatedAt: base.Add(2 * time.Minute)},
		{SessionID: "sess_newest", ActorSubject: "local-dev", Agent: "unit-tests", Classification: "internal", PromptSHA256: "newest", Status: "running", CreatedAt: base.Add(3 * time.Minute)},
	}
	for _, record := range records {
		if err := store.Create(ctx, record); err != nil {
			t.Fatalf("create %s: %v", record.SessionID, err)
		}
	}

	got, err := store.ListRecentSince(ctx, "local-dev", base, 10)
	if err != nil {
		t.Fatalf("list recent since: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two in-window local-dev sessions, got %d: %#v", len(got), got)
	}
	if got[0].SessionID != "sess_newest" || got[1].SessionID != "sess_in_window" {
		t.Fatalf("expected newest-first in-window sessions, got %#v", got)
	}

	limited, err := store.ListRecentSince(ctx, "local-dev", base, 1)
	if err != nil {
		t.Fatalf("limited list recent since: %v", err)
	}
	if len(limited) != 1 || limited[0].SessionID != "sess_newest" {
		t.Fatalf("expected newest session only, got %#v", limited)
	}
}

func TestSQLiteSessionStoreNotFound(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sessions.db"

	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	_, err = store.Get(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected not found")
	}
}

func TestSQLiteSessionStoreReadsLegacyRowsAfterMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			actor_subject TEXT NOT NULL,
			agent TEXT NOT NULL,
			classification TEXT NOT NULL,
			prompt_sha256 TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		INSERT INTO sessions (
			session_id, actor_subject, agent, classification, prompt_sha256, status, created_at
		) VALUES (
			'sess_legacy', 'local-dev', 'unit-tests', 'internal', 'abc123', 'created', ?
		);
	`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		_ = db.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	got, err := store.Get(context.Background(), "sess_legacy")
	if err != nil {
		t.Fatalf("get legacy session: %v", err)
	}
	if got.SessionID != "sess_legacy" {
		t.Fatalf("unexpected session id: %s", got.SessionID)
	}
	if got.ParentSessionID != "" || got.UseCaseID != "" || got.StoryPoints != 0 || got.BaselineCostUSD != 0 {
		t.Fatalf("expected zero-value migrated fields, got %#v", got)
	}
}

func TestSQLiteSessionStoreCreatesDirectoryAndRestrictsPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "sessions.db")

	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat session db: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected session db permissions 0600, got %o", got)
	}
}

func TestSQLiteSessionStoreRequiresPath(t *testing.T) {
	if _, err := NewSQLiteSessionStore(""); err == nil {
		t.Fatal("expected empty path to fail")
	}
}

// TestSQLiteSessionStoreClaimedIdentityColumnsMigrateIdempotently reopens the
// same database twice, exercising ensureSessionColumn's ALTER TABLE path on
// the second open. It must not error and the columns must keep behaving like
// any other migrated column (default empty string, no data loss).
func TestSQLiteSessionStoreClaimedIdentityColumnsMigrateIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")

	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	if err := store.Create(ctx, SessionRecord{
		SessionID:          "sess_claimed",
		ActorSubject:       "local-dev",
		Agent:              "unit-tests",
		Classification:     "internal",
		PromptSHA256:       "abc123",
		Status:             "running",
		CreatedAt:          time.Now().UTC(),
		ClaimedOSUsername:  "alice",
		ClaimedHostname:    "alice-laptop",
		ClaimedGithubLogin: "alice-gh",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening re-runs migrate() and ensureSessionColumn against a schema
	// that already has the claimed_* columns; this must be a no-op, not an error.
	reopened, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.Get(ctx, "sess_claimed")
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if got.ClaimedOSUsername != "alice" || got.ClaimedHostname != "alice-laptop" || got.ClaimedGithubLogin != "alice-gh" {
		t.Fatalf("expected claimed identity to survive re-migration, got %#v", got)
	}
}

func TestSQLiteSessionStoreUpdateClaimedIdentityIfEmptyFillsOnlyEmptyFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Create(ctx, SessionRecord{
		SessionID:         "sess_fill",
		ActorSubject:      "local-dev",
		Agent:             "unit-tests",
		Classification:    "internal",
		PromptSHA256:      "abc123",
		Status:            "running",
		CreatedAt:         time.Now().UTC(),
		ClaimedOSUsername: "alice",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// os_username is already set to "alice"; the update must not overwrite it
	// even though this call reports "mallory". hostname and github_login are
	// still empty, so they should be filled in.
	if err := store.UpdateClaimedIdentityIfEmpty(ctx, "sess_fill", "mallory", "alice-laptop", "alice-gh"); err != nil {
		t.Fatalf("update claimed identity: %v", err)
	}

	got, err := store.Get(ctx, "sess_fill")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ClaimedOSUsername != "alice" {
		t.Fatalf("expected claimed os_username to stay alice, got %q", got.ClaimedOSUsername)
	}
	if got.ClaimedHostname != "alice-laptop" || got.ClaimedGithubLogin != "alice-gh" {
		t.Fatalf("expected empty fields to be filled in, got %#v", got)
	}

	// A second call with different values must not change anything further:
	// every field is now non-empty.
	if err := store.UpdateClaimedIdentityIfEmpty(ctx, "sess_fill", "bob", "bob-laptop", "bob-gh"); err != nil {
		t.Fatalf("second update claimed identity: %v", err)
	}
	got2, err := store.Get(ctx, "sess_fill")
	if err != nil {
		t.Fatalf("get after second update: %v", err)
	}
	if got2.ClaimedOSUsername != "alice" || got2.ClaimedHostname != "alice-laptop" || got2.ClaimedGithubLogin != "alice-gh" {
		t.Fatalf("expected fill-in-once to never overwrite recorded values, got %#v", got2)
	}
}

func TestSQLiteSessionStoreIdentityMapGroupsFiltersAndIncludesDone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	base := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	records := []SessionRecord{
		// Older session for the same (actor, os_username, github_login) group;
		// the newer session below should win for last_seen and hostname.
		{
			SessionID: "sess_alice_1", ActorSubject: "dev@example.test", Agent: "managed-client",
			Classification: "internal", PromptSHA256: "", Status: "done", CreatedAt: base,
			ClaimedOSUsername: "alice", ClaimedHostname: "alice-old-host", ClaimedGithubLogin: "alice-gh",
		},
		{
			SessionID: "sess_alice_2", ActorSubject: "dev@example.test", Agent: "managed-client",
			Classification: "internal", PromptSHA256: "", Status: "done", CreatedAt: base.Add(time.Hour),
			ClaimedOSUsername: "alice", ClaimedHostname: "alice-new-host", ClaimedGithubLogin: "alice-gh",
		},
		// A test-connection style session that ends immediately (status
		// "done") must still be included.
		{
			SessionID: "sess_bob_done", ActorSubject: "dev2@example.test", Agent: "managed-client",
			Classification: "internal", PromptSHA256: "", Status: "done", CreatedAt: base.Add(2 * time.Hour),
			ClaimedOSUsername: "bob", ClaimedHostname: "bob-host", ClaimedGithubLogin: "",
		},
		// No claimed OS username: must be excluded entirely.
		{
			SessionID: "sess_no_identity", ActorSubject: "dev3@example.test", Agent: "managed-client",
			Classification: "internal", PromptSHA256: "", Status: "running", CreatedAt: base.Add(3 * time.Hour),
		},
	}
	for _, rec := range records {
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("create %s: %v", rec.SessionID, err)
		}
	}

	rows, err := store.IdentityMap(ctx)
	if err != nil {
		t.Fatalf("identity map: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two grouped rows, got %d: %#v", len(rows), rows)
	}

	byActor := map[string]IdentityMapRow{}
	for _, row := range rows {
		byActor[row.ActorSubject] = row
	}

	alice, ok := byActor["dev@example.test"]
	if !ok {
		t.Fatalf("expected a grouped row for alice, got %#v", rows)
	}
	if alice.ClaimedOSUsername != "alice" || alice.ClaimedGithubLogin != "alice-gh" {
		t.Fatalf("unexpected alice group: %#v", alice)
	}
	if alice.ClaimedHostname != "alice-new-host" {
		t.Fatalf("expected hostname from most recent session, got %q", alice.ClaimedHostname)
	}
	if !alice.LastSeen.Equal(base.Add(time.Hour)) {
		t.Fatalf("expected last_seen to be the max created_at, got %s", alice.LastSeen)
	}

	bob, ok := byActor["dev2@example.test"]
	if !ok {
		t.Fatalf("expected a grouped row for bob (done session included), got %#v", rows)
	}
	if bob.ClaimedGithubLogin != "" {
		t.Fatalf("expected empty github_login to be included, got %q", bob.ClaimedGithubLogin)
	}

	if _, excluded := byActor["dev3@example.test"]; excluded {
		t.Fatalf("expected session with no claimed_os_username to be excluded, got %#v", rows)
	}
}
