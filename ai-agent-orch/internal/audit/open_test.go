package audit

import (
	"path/filepath"
	"testing"
)

func TestIsSQLitePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "audit.db", want: true},
		{path: "audit.sqlite", want: true},
		{path: "AUDIT.SQLITE3", want: true},
		{path: "audit.jsonl", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsSQLitePath(tt.path); got != tt.want {
				t.Fatalf("IsSQLitePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestNewStoreSelectsSQLiteForDatabasePath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sqliteStore, ok := store.(*SQLiteStore)
	if !ok {
		t.Fatalf("expected SQLiteStore, got %T", store)
	}
	defer sqliteStore.Close()
}

func TestNewStoreSelectsFileStoreForJSONLPath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, ok := store.(*FileStore); !ok {
		t.Fatalf("expected FileStore, got %T", store)
	}
}
