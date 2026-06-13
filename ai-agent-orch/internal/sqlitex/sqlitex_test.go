package sqlitex

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open("", "test-store"); err == nil {
		t.Fatal("Open with empty path should fail")
	} else if !strings.Contains(err.Error(), "test-store") {
		t.Fatalf("error should name the store label, got: %v", err)
	}
}

func TestOpenCreatesDirectoryAndConfiguresWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state", "store.db")
	db, err := Open(path, "test-store")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}

	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec("INSERT INTO t (v) VALUES (?)", "hello"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var v string
	if err := db.QueryRow("SELECT v FROM t WHERE id = 1").Scan(&v); err != nil {
		t.Fatalf("select: %v", err)
	}
	if v != "hello" {
		t.Fatalf("v = %q, want hello", v)
	}
}
