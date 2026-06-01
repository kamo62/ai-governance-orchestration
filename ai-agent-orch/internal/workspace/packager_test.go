package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPackager(t *testing.T) {
	root := t.TempDir()
	// Create some files.
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(root, "README.md"), []byte("# Hello"), 0o644)
	os.MkdirAll(filepath.Join(root, "node_modules", "foo"), 0o755)
	os.WriteFile(filepath.Join(root, "node_modules", "foo", "index.js"), []byte("module.exports"), 0o644)
	os.WriteFile(filepath.Join(root, "big.bin"), make([]byte, 300*1024), 0o644)

	p := DefaultPackager(root)
	entries, err := p.Package()
	if err != nil {
		t.Fatalf("package failed: %v", err)
	}

	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}

	if !contains(paths, "main.go") {
		t.Errorf("expected main.go in package, got %v", paths)
	}
	if !contains(paths, "README.md") {
		t.Errorf("expected README.md in package, got %v", paths)
	}
	if contains(paths, "node_modules/foo/index.js") {
		t.Errorf("expected node_modules excluded, got %v", paths)
	}
	if contains(paths, "big.bin") {
		t.Errorf("expected big.bin excluded by size, got %v", paths)
	}
}

func TestPackagerAsContext(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0o644)

	p := DefaultPackager(root)
	ctx, err := p.PackageAsContext()
	if err != nil {
		t.Fatalf("package as context failed: %v", err)
	}
	if !strings.Contains(ctx, "main.go") {
		t.Errorf("expected context to contain main.go: %s", ctx)
	}
	if !strings.Contains(ctx, "package main") {
		t.Errorf("expected context to contain file content: %s", ctx)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
