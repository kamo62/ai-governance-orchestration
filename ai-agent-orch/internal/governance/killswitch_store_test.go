package governance

import (
	"path/filepath"
	"testing"
)

func TestSQLiteKillSwitchSurvivesReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "audit.db")

	store, err := NewSQLiteKillSwitch(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteKillSwitch: %v", err)
	}
	if err := store.Block("agent", "unit-tests"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if !store.IsBlocked("agent", "unit-tests") {
		t.Fatal("expected agent to be blocked")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := NewSQLiteKillSwitch(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if !reopened.IsBlocked("agent", "unit-tests") {
		t.Fatal("expected kill switch to survive restart")
	}
	if err := reopened.Unblock("agent", "unit-tests"); err != nil {
		t.Fatalf("Unblock: %v", err)
	}
	if reopened.IsBlocked("agent", "unit-tests") {
		t.Fatal("expected agent to be unblocked")
	}
	list := reopened.List()
	if ids := list["agent"]; len(ids) != 0 {
		t.Fatalf("expected empty list, got %v", ids)
	}
}
