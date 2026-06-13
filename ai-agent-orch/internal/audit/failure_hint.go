package audit

import (
	"strings"
)

// FailureHint returns operator guidance for a failed audit append.
func FailureHint(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "database is locked"), strings.Contains(msg, "sqlite_busy"):
		return "SQLite is temporarily locked, often right after compose up while the audit DB initializes. Wait for GET /readyz to succeed, then retry. If it persists, ensure only one governance-shell instance is writing the audit volume."
	case strings.Contains(msg, "concurrent append conflict"),
		strings.Contains(msg, "prev hash"),
		strings.Contains(msg, "hash chain"),
		strings.Contains(msg, "audit chain head"):
		return "Audit hash chain conflict, usually from a stale Docker volume built against an older image. Rebuild governance-shell and orchestrator, then reset audit state with: docker compose down -v"
	default:
		return "Audit persistence failed. Rebuild governance-shell and orchestrator images, wait for GET /readyz, then retry. If it persists, reset audit volumes with: docker compose down -v"
	}
}
