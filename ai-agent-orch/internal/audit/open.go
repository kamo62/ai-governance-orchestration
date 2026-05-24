package audit

import "strings"

// NewStore selects the audit backend from the configured path.
func NewStore(path string) (Store, error) {
	if IsSQLitePath(path) {
		return NewSQLiteStore(path)
	}
	return NewFileStore(path), nil
}

// IsSQLitePath reports whether the configured audit path should use SQLite.
func IsSQLitePath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sqlite") || strings.HasSuffix(lower, ".sqlite3")
}
