package composition

import (
	"testing"
)

func TestCompositionFlow(t *testing.T) {
	stages := []Stage{
		{Name: "investigate", Agent: "test-generation"},
		{Name: "plan", Agent: "architecture-review"},
	}
	c := NewComposition("sess_123", stages)

	if c.CurrentIdx != 0 {
		t.Fatalf("expected current idx 0, got %d", c.CurrentIdx)
	}

	// Cannot advance before completing stage 0.
	if err := c.Advance(); err == nil {
		t.Fatal("expected error when advancing incomplete stage")
	}

	// Complete stage 0.
	if err := c.CompleteStage("investigation output"); err != nil {
		t.Fatalf("complete stage: %v", err)
	}

	// Still cannot advance without approval.
	if err := c.Advance(); err != ErrHumanGateRequired {
		t.Fatalf("expected human gate error, got %v", err)
	}

	// Approve and advance.
	if err := c.ApproveStage(); err != nil {
		t.Fatalf("approve stage: %v", err)
	}
	if err := c.Advance(); err != nil {
		t.Fatalf("advance: %v", err)
	}

	if c.CurrentIdx != 1 {
		t.Fatalf("expected current idx 1, got %d", c.CurrentIdx)
	}
	if c.Stages[1].Input != "investigation output" {
		t.Fatalf("expected input carried forward, got %q", c.Stages[1].Input)
	}

	// Complete stage 1.
	if err := c.CompleteStage("plan output"); err != nil {
		t.Fatalf("complete stage: %v", err)
	}
	if err := c.ApproveStage(); err != nil {
		t.Fatalf("approve stage: %v", err)
	}

	// Cannot advance past max depth of 2 (already at idx 1, next would be 2 which equals maxDepth).
	if err := c.Advance(); err != ErrMaxDepthExceeded {
		t.Fatalf("expected max depth error, got %v", err)
	}
}

func TestValidateStages(t *testing.T) {
	if err := ValidateStages([]Stage{}, 2); err == nil {
		t.Fatal("expected error for empty stages")
	}
	if err := ValidateStages([]Stage{{Name: "a", Agent: "x"}, {Name: "b", Agent: "y"}, {Name: "c", Agent: "z"}}, 2); err == nil {
		t.Fatal("expected error for stages exceeding max depth")
	}
	if err := ValidateStages([]Stage{{Name: "a"}}, 2); err == nil {
		t.Fatal("expected error for missing agent")
	}
	if err := ValidateStages([]Stage{{Agent: "x"}}, 2); err == nil {
		t.Fatal("expected error for missing name")
	}
	if err := ValidateStages([]Stage{{Name: "a", Agent: "x"}}, 2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompositionStore(t *testing.T) {
	store := NewCompositionStore()
	c := NewComposition("sess_1", []Stage{{Name: "s1", Agent: "a1"}})
	store.Set("sess_1", c)

	got, ok := store.Get("sess_1")
	if !ok {
		t.Fatal("expected composition to be found")
	}
	if got.SessionID != "sess_1" {
		t.Fatalf("expected session sess_1, got %q", got.SessionID)
	}

	store.Delete("sess_1")
	if _, ok := store.Get("sess_1"); ok {
		t.Fatal("expected composition to be deleted")
	}
}
