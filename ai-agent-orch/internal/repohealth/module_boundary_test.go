package repohealth

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoPackageDiscoveryDoesNotTraverseNodeModules(t *testing.T) {
	moduleRoot := findModuleRoot(t)

	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", "./...")
	cmd.Dir = moduleRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list ./... failed: %v\n%s", err, out)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(moduleRoot, line)
		if err != nil {
			t.Fatalf("make %q relative to %q: %v", line, moduleRoot, err)
		}
		if strings.Contains(filepath.ToSlash(rel), "/node_modules/") {
			t.Fatalf("go package discovery traversed node_modules package %q; add a module boundary around non-Go dependency trees", rel)
		}
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root")
		}
		dir = parent
	}
}
