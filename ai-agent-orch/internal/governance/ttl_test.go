package governance

import (
	"testing"
	"time"
)

func TestSessionService_PromptEviction(t *testing.T) {
	s := NewSessionService(SessionConfig{
		LocalStateTTL: 100 * time.Millisecond,
	})
	s.rememberPrompt("sess_1", "hello")

	_, ok := s.promptForSession("sess_1")
	if !ok {
		t.Fatal("prompt should exist immediately")
	}

	time.Sleep(150 * time.Millisecond)

	_, ok = s.promptForSession("sess_1")
	if ok {
		t.Fatal("prompt should be evicted after TTL")
	}
}

func TestSessionService_PatchEviction(t *testing.T) {
	s := NewSessionService(SessionConfig{
		LocalStateTTL: 100 * time.Millisecond,
	})
	s.rememberPatch("sess_1", "patch_1")

	if !s.patchKnown("sess_1", "patch_1") {
		t.Fatal("patch should be known immediately")
	}

	time.Sleep(150 * time.Millisecond)

	if s.patchKnown("sess_1", "patch_1") {
		t.Fatal("patch should be evicted after TTL")
	}
}

func TestSessionService_PatchEvictionKeepsFreshPatch(t *testing.T) {
	s := NewSessionService(SessionConfig{
		LocalStateTTL: time.Minute,
	})
	s.rememberPatch("sess_1", "patch_old")
	s.rememberPatch("sess_1", "patch_new")

	s.patchMu.Lock()
	s.patchTimes["sess_1"]["patch_old"] = time.Now().UTC().Add(-2 * time.Minute)
	s.patchMu.Unlock()

	if s.patchKnown("sess_1", "patch_old") {
		t.Fatal("old patch should be evicted")
	}
	if !s.patchKnown("sess_1", "patch_new") {
		t.Fatal("fresh patch should remain known")
	}
}

func TestSessionService_ForgetPromptRemovesTimestamp(t *testing.T) {
	s := NewSessionService(SessionConfig{
		LocalStateTTL: 30 * time.Minute,
	})
	s.rememberPrompt("sess_1", "hello")
	s.forgetPrompt("sess_1")

	if _, ok := s.promptTimes["sess_1"]; ok {
		t.Fatal("forgetPrompt should remove timestamp")
	}
}

func TestSessionService_CancelEviction(t *testing.T) {
	s := NewSessionService(SessionConfig{
		LocalStateTTL: 100 * time.Millisecond,
	})
	var called bool
	s.registerCancel("sess_1", func() {
		called = true
	})

	time.Sleep(150 * time.Millisecond)

	s.cancelExecution("sess_1")
	if called {
		t.Fatal("expired cancel func should be evicted before cancellation")
	}
}

func TestPatchBuffer_Eviction(t *testing.T) {
	b := NewPatchBuffer()
	b.ttl = 100 * time.Millisecond

	ctx := t.Context()
	payload := `{"patchId":"p1","sessionId":"sess_1","files":[{"path":"foo.txt","action":"create","newContent":"hello"}]}`
	_, err := b.Store(ctx, "sess_1", payload)
	if err != nil {
		t.Fatalf("store failed: %v", err)
	}

	_, err = b.Get(ctx, "sess_1", "p1")
	if err != nil {
		t.Fatalf("get immediately failed: %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	_, err = b.Get(ctx, "sess_1", "p1")
	if err == nil {
		t.Fatal("expected eviction after TTL")
	}
}

func TestPatchBuffer_EvictsPerPatch(t *testing.T) {
	b := NewPatchBuffer()
	b.ttl = time.Minute
	ctx := t.Context()

	oldPayload := `{"patchId":"old","sessionId":"sess_1","files":[{"path":"old.txt","action":"create","newContent":"old"}]}`
	newPayload := `{"patchId":"new","sessionId":"sess_1","files":[{"path":"new.txt","action":"create","newContent":"new"}]}`
	if _, err := b.Store(ctx, "sess_1", oldPayload); err != nil {
		t.Fatalf("store old patch: %v", err)
	}
	if _, err := b.Store(ctx, "sess_1", newPayload); err != nil {
		t.Fatalf("store new patch: %v", err)
	}

	b.mu.Lock()
	b.times["sess_1"]["old"] = time.Now().UTC().Add(-2 * time.Minute)
	b.mu.Unlock()

	if _, err := b.Get(ctx, "sess_1", "old"); err == nil {
		t.Fatal("expected old patch to be evicted")
	}
	if _, err := b.Get(ctx, "sess_1", "new"); err != nil {
		t.Fatalf("fresh patch should remain: %v", err)
	}
}
