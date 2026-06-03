package appversion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionMatchesRootVersionFile(t *testing.T) {
	versionPath := findRootVersionFile(t)
	data, err := os.ReadFile(versionPath)
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}

	if got := strings.TrimSpace(string(data)); got != Version {
		t.Fatalf("appversion.Version mismatch: VERSION has %q, code has %q", got, Version)
	}
}

func findRootVersionFile(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "VERSION")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("VERSION file not found while walking up from package directory")
	return ""
}
