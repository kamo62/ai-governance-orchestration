package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCanonicalHash_Deterministic(t *testing.T) {
	e := Event{
		EventID:    "evt_1",
		EventType:  "session.created",
		Actor:      "local-dev",
		RecordedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	h1 := canonicalHash(e)
	h2 := canonicalHash(e)
	if h1 != h2 {
		t.Fatalf("canonical hash not deterministic: %s vs %s", h1, h2)
	}
	if h1 == "" || h1 == "sha256:ERR" {
		t.Fatalf("unexpected hash: %s", h1)
	}
}

func TestCanonicalHash_ExcludesEventHash(t *testing.T) {
	e := Event{
		EventID:    "evt_1",
		EventType:  "session.created",
		Actor:      "local-dev",
		RecordedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	h1 := canonicalHash(e)
	e.EventHash = "sha256:fake"
	h2 := canonicalHash(e)
	if h1 != h2 {
		t.Fatalf("canonical hash should ignore EventHash: %s vs %s", h1, h2)
	}
}

func TestCanonicalHash_DetectsMutation(t *testing.T) {
	e1 := Event{
		EventID:    "evt_1",
		EventType:  "session.created",
		Actor:      "local-dev",
		RecordedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	e2 := e1
	e2.Actor = "attacker"

	h1 := canonicalHash(e1)
	h2 := canonicalHash(e2)
	if h1 == h2 {
		t.Fatal("canonical hash should differ for mutated event")
	}
}

func TestChainAppender_LinksEvents(t *testing.T) {
	fs := NewFileStore(t.TempDir() + "/audit.jsonl")
	ca := NewChainAppender(fs)
	ctx := context.Background()

	e1, err := ca.Append(ctx, Event{EventID: "evt_1", EventType: "session.created", SessionID: "sess_a", Actor: "local-dev"})
	if err != nil {
		t.Fatalf("append e1: %v", err)
	}
	if e1.PrevEventHash != "" {
		t.Fatalf("first event should have empty prev hash, got %s", e1.PrevEventHash)
	}
	if e1.EventHash == "" {
		t.Fatal("first event should have a hash")
	}

	e2, err := ca.Append(ctx, Event{EventID: "evt_2", EventType: "router.specialist.selected", SessionID: "sess_a", Actor: "local-dev"})
	if err != nil {
		t.Fatalf("append e2: %v", err)
	}
	if e2.PrevEventHash != e1.EventHash {
		t.Fatalf("second event prev hash mismatch: expected %s, got %s", e1.EventHash, e2.PrevEventHash)
	}
	if e2.EventHash == "" {
		t.Fatal("second event should have a hash")
	}
	if e2.EventHash == e1.EventHash {
		t.Fatal("event hashes should differ")
	}
}

func TestChainAppender_SeedsLatestHashAcrossWrappers(t *testing.T) {
	fs := NewFileStore(t.TempDir() + "/audit.jsonl")
	first := NewChainAppender(fs)
	second := NewChainAppender(fs)
	ctx := context.Background()

	e1, err := first.Append(ctx, Event{EventID: "evt_1", EventType: "session.created", SessionID: "sess_a", Actor: "local-dev"})
	if err != nil {
		t.Fatalf("append e1: %v", err)
	}
	e2, err := second.Append(ctx, Event{EventID: "evt_2", EventType: "router.specialist.selected", SessionID: "sess_a", Actor: "local-dev"})
	if err != nil {
		t.Fatalf("append e2: %v", err)
	}
	if e2.PrevEventHash != e1.EventHash {
		t.Fatalf("second wrapper did not seed previous hash: expected %s, got %s", e1.EventHash, e2.PrevEventHash)
	}

	e3, err := first.Append(ctx, Event{EventID: "evt_3", EventType: "session.confirmed", SessionID: "sess_a", Actor: "local-dev"})
	if err != nil {
		t.Fatalf("append e3: %v", err)
	}
	if e3.PrevEventHash != e2.EventHash {
		t.Fatalf("stale wrapper did not refresh previous hash: expected %s, got %s", e2.EventHash, e3.PrevEventHash)
	}

	events, err := fs.EventsBySession(ctx, "sess_a")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if err := VerifyChain(events); err != nil {
		t.Fatalf("verify chain: %v", err)
	}
}

func TestChainAppender_IsolatesSessions(t *testing.T) {
	fs := NewFileStore(t.TempDir() + "/audit.jsonl")
	ca := NewChainAppender(fs)
	ctx := context.Background()

	e1a, _ := ca.Append(ctx, Event{EventID: "evt_1a", EventType: "session.created", SessionID: "sess_a", Actor: "local-dev"})
	e1b, _ := ca.Append(ctx, Event{EventID: "evt_1b", EventType: "session.created", SessionID: "sess_b", Actor: "local-dev"})

	e2a, _ := ca.Append(ctx, Event{EventID: "evt_2a", EventType: "router.specialist.selected", SessionID: "sess_a", Actor: "local-dev"})
	e2b, _ := ca.Append(ctx, Event{EventID: "evt_2b", EventType: "router.specialist.selected", SessionID: "sess_b", Actor: "local-dev"})

	if e2a.PrevEventHash != e1a.EventHash {
		t.Fatalf("session_a chain broken")
	}
	if e2b.PrevEventHash != e1b.EventHash {
		t.Fatalf("session_b chain broken")
	}
	if e1a.EventHash == e1b.EventHash {
		t.Fatal("different sessions should have different first hashes")
	}
}

func TestChainAppender_DelegatesRetentionPolicy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer store.Close()
	chain := NewChainAppender(store)
	ctx := context.Background()

	_, err = chain.Append(ctx, Event{
		EventID:    "evt_old",
		EventType:  "test.old",
		SessionID:  "sess_retention",
		Actor:      "local-dev",
		RecordedAt: time.Now().UTC().Add(-48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("append old event: %v", err)
	}
	_, err = chain.Append(ctx, Event{
		EventID:    "evt_recent",
		EventType:  "test.recent",
		SessionID:  "sess_retention",
		Actor:      "local-dev",
		RecordedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("append recent event: %v", err)
	}

	purged, err := chain.RetentionPolicy(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("retention policy: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 purged event, got %d", purged)
	}
	events, err := chain.EventsBySession(ctx, "sess_retention")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if len(events) != 1 || events[0].EventID != "evt_recent" {
		t.Fatalf("unexpected remaining events: %#v", events)
	}
}

func TestChainAppender_NoSessionNoChain(t *testing.T) {
	fs := NewFileStore(t.TempDir() + "/audit.jsonl")
	ca := NewChainAppender(fs)
	ctx := context.Background()

	e1, err := ca.Append(ctx, Event{EventID: "evt_1", EventType: "session.denied", Actor: "local-dev"})
	if err != nil {
		t.Fatalf("append e1: %v", err)
	}
	if e1.PrevEventHash != "" {
		t.Fatalf("no-session event should have empty prev hash, got %s", e1.PrevEventHash)
	}
}

func TestVerifyChain_Valid(t *testing.T) {
	fs := NewFileStore(t.TempDir() + "/audit.jsonl")
	ca := NewChainAppender(fs)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := ca.Append(ctx, Event{
			EventID:   "evt_" + string(rune('1'+i)),
			EventType: "test",
			SessionID: "sess_a",
			Actor:     "local-dev",
		})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	events, err := fs.EventsBySession(ctx, "sess_a")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if err := VerifyChain(events); err != nil {
		t.Fatalf("verify chain: %v", err)
	}
}

func TestVerifyChain_DetectsEdit(t *testing.T) {
	fs := NewFileStore(t.TempDir() + "/audit.jsonl")
	ca := NewChainAppender(fs)
	ctx := context.Background()

	_, err := ca.Append(ctx, Event{EventID: "evt_1", EventType: "test", SessionID: "sess_a", Actor: "local-dev"})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	events, _ := fs.EventsBySession(ctx, "sess_a")
	events[0].Actor = "tampered"

	if err := VerifyChain(events); err == nil {
		t.Fatal("verify chain should detect edit")
	}
}

func TestVerifyChain_DetectsDeletion(t *testing.T) {
	fs := NewFileStore(t.TempDir() + "/audit.jsonl")
	ca := NewChainAppender(fs)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := ca.Append(ctx, Event{EventID: "evt_" + string(rune('1'+i)), EventType: "test", SessionID: "sess_a", Actor: "local-dev"})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	events, _ := fs.EventsBySession(ctx, "sess_a")
	// Remove middle event.
	events = append(events[:1], events[2:]...)

	if err := VerifyChain(events); err == nil {
		t.Fatal("verify chain should detect deletion")
	}
}

func TestVerifyChain_DetectsReorder(t *testing.T) {
	fs := NewFileStore(t.TempDir() + "/audit.jsonl")
	ca := NewChainAppender(fs)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := ca.Append(ctx, Event{EventID: "evt_" + string(rune('1'+i)), EventType: "test", SessionID: "sess_a", Actor: "local-dev"})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	events, _ := fs.EventsBySession(ctx, "sess_a")
	// Swap first and second.
	events[0], events[1] = events[1], events[0]

	if err := VerifyChain(events); err == nil {
		t.Fatal("verify chain should detect reorder")
	}
}

func TestVerifyChain_DetectsInsertionWithWrongPrevHash(t *testing.T) {
	fs := NewFileStore(t.TempDir() + "/audit.jsonl")
	ca := NewChainAppender(fs)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		_, err := ca.Append(ctx, Event{EventID: "evt_" + string(rune('1'+i)), EventType: "test", SessionID: "sess_a", Actor: "local-dev"})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	events, _ := fs.EventsBySession(ctx, "sess_a")
	// Insert a fake event with wrong prev hash.
	fake := events[0]
	fake.EventID = "evt_fake"
	fake.PrevEventHash = "sha256:wrong"
	fake.EventHash = canonicalHash(fake)
	events = append([]Event{events[0], fake}, events[1:]...)

	if err := VerifyChain(events); err == nil {
		t.Fatal("verify chain should detect insertion with wrong prev hash")
	}
}

func TestVerifyChain_EmptyOK(t *testing.T) {
	if err := VerifyChain(nil); err != nil {
		t.Fatalf("empty chain should verify: %v", err)
	}
}
