package main

import (
	"os"
	"path/filepath"
	"testing"

	"ai-agent-orch/internal/audit"
)

func TestHasSQLiteExt(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "plain db", path: "audit.db", want: true},
		{name: "sqlite", path: "audit.sqlite", want: true},
		{name: "sqlite3 uppercase", path: "AUDIT.SQLITE3", want: true},
		{name: "short non-match", path: "db", want: false},
		{name: "jsonl", path: "audit.jsonl", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasSQLiteExt(tt.path); got != tt.want {
				t.Fatalf("hasSQLiteExt(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestNewAuditStoreUsesSQLiteForDatabasePath(t *testing.T) {
	store, err := newAuditStore(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("new audit store: %v", err)
	}
	sqliteStore, ok := store.(*audit.SQLiteStore)
	if !ok {
		t.Fatalf("expected sqlite audit store, got %T", store)
	}
	defer sqliteStore.Close()
}

func TestNewAuditStoreReturnsSQLiteErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("create directory at db path: %v", err)
	}
	if _, err := newAuditStore(path); err == nil {
		t.Fatal("expected sqlite audit store error")
	}
}
