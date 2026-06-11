package governance

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// SQLiteKillSwitch persists kill-switch state so an admin block survives
// process restarts. Reads are served from a write-through in-memory copy;
// the database is only touched on state changes and startup.
type SQLiteKillSwitch struct {
	db  *sql.DB
	mem *MemoryKillSwitch
}

func NewSQLiteKillSwitch(dbPath string) (*SQLiteKillSwitch, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open kill switch db: %w", err)
	}
	store := &SQLiteKillSwitch{db: db, mem: NewMemoryKillSwitch()}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.load(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteKillSwitch) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS kill_switches (
			scope TEXT NOT NULL,
			id TEXT NOT NULL,
			PRIMARY KEY (scope, id)
		);
	`)
	if err != nil {
		return fmt.Errorf("migrate kill switch table: %w", err)
	}
	return nil
}

func (s *SQLiteKillSwitch) load() error {
	rows, err := s.db.Query(`SELECT scope, id FROM kill_switches`)
	if err != nil {
		return fmt.Errorf("load kill switches: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var scope, id string
		if err := rows.Scan(&scope, &id); err != nil {
			return fmt.Errorf("scan kill switch row: %w", err)
		}
		_ = s.mem.Block(scope, id)
	}
	return rows.Err()
}

func (s *SQLiteKillSwitch) IsBlocked(scope, id string) bool {
	if s == nil {
		return false
	}
	return s.mem.IsBlocked(scope, id)
}

func (s *SQLiteKillSwitch) Block(scope, id string) error {
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO kill_switches (scope, id) VALUES (?, ?)`, scope, id); err != nil {
		return fmt.Errorf("persist kill switch: %w", err)
	}
	return s.mem.Block(scope, id)
}

func (s *SQLiteKillSwitch) Unblock(scope, id string) error {
	if _, err := s.db.Exec(`DELETE FROM kill_switches WHERE scope = ? AND id = ?`, scope, id); err != nil {
		return fmt.Errorf("remove kill switch: %w", err)
	}
	return s.mem.Unblock(scope, id)
}

func (s *SQLiteKillSwitch) List() map[string][]string {
	return s.mem.List()
}

func (s *SQLiteKillSwitch) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
