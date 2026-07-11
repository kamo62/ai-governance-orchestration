// Package sqlitex centralizes how local SQLite state stores are opened so
// every store shares the same pragmas, pooling, and directory handling.
package sqlitex

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) the SQLite database at path, creating the parent
// directory when needed. label names the store in error messages.
//
// Every store gets WAL journaling for concurrent reads alongside a single
// writer, a 10s busy timeout so writers wait instead of failing under
// contention, and a bounded connection pool to avoid churn.
func Open(path string, label string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("%s db path is required", label)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create %s db directory: %w", label, err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s db: %w", label, err)
	}
	if path == ":memory:" || strings.Contains(path, "mode=memory") {
		// Each pooled connection to an in-memory DSN opens its own private
		// database, so the schema would exist on only one of them.
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(8)
		db.SetMaxIdleConns(4)
		db.SetConnMaxLifetime(30 * time.Minute)
	}
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 10000;
		PRAGMA synchronous = NORMAL;
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure %s db: %w", label, err)
	}
	return db, nil
}

// TableColumns materializes column names and closes the PRAGMA rows before
// callers execute schema-changing statements on the same connection.
func TableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			_ = rows.Close()
			return nil, err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return columns, nil
}
