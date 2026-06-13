package repohealth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoModulePathMatchesCanonicalRepository(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	data, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	const want = "module github.com/kamo62/ai-governance-orchestration/ai-agent-orch"
	firstLine := strings.SplitN(string(data), "\n", 2)[0]
	if firstLine != want {
		t.Fatalf("go.mod module path = %q, want %q", firstLine, want)
	}
}
