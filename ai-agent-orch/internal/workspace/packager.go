package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Packager collects workspace source context for CLI runs.
type Packager struct {
	RootPath string
	// IncludePatterns are glob patterns for files to include (e.g., "*.go", "*.ts").
	IncludePatterns []string
	// ExcludePatterns are glob patterns for files to skip (e.g., "node_modules/**", "*.tmp").
	ExcludePatterns []string
	// MaxFiles caps the number of files included.
	MaxFiles int
	// MaxFileSizeBytes caps the size of a single file.
	MaxFileSizeBytes int64
	// MaxTotalBytes caps the total payload size.
	MaxTotalBytes int64
}

// DefaultPackager returns a packager with sensible defaults for a Go/TS project.
func DefaultPackager(root string) *Packager {
	return &Packager{
		RootPath:         root,
		IncludePatterns:  []string{"*.go", "*.ts", "*.tsx", "*.js", "*.jsx", "*.md", "*.yaml", "*.yml", "*.json"},
		ExcludePatterns:  []string{"node_modules/**", "vendor/**", ".git/**", "*.tmp", "*.log", "dist/**", "build/**", "out/**", "*.vsix"},
		MaxFiles:         50,
		MaxFileSizeBytes: 256 * 1024,      // 256 KiB per file
		MaxTotalBytes:    2 * 1024 * 1024, // 2 MiB total
	}
}

// FileEntry is a single packaged file.
type FileEntry struct {
	Path    string
	Content string
}

// Package walks the workspace and returns included files.
func (p *Packager) Package() ([]FileEntry, error) {
	if p.RootPath == "" {
		return nil, fmt.Errorf("workspace root path is required")
	}
	info, err := os.Stat(p.RootPath)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root must be a directory: %s", p.RootPath)
	}

	var entries []FileEntry
	var totalBytes int64

	err = filepath.Walk(p.RootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(p.RootPath, path)
		if err != nil {
			return nil
		}
		// Normalize to forward slashes for consistent matching.
		rel = filepath.ToSlash(rel)

		if p.isExcluded(rel) {
			return nil
		}
		if !p.isIncluded(rel) {
			return nil
		}
		if p.MaxFiles > 0 && len(entries) >= p.MaxFiles {
			return filepath.SkipDir
		}
		if p.MaxFileSizeBytes > 0 && info.Size() > p.MaxFileSizeBytes {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}
		if p.MaxTotalBytes > 0 && totalBytes+int64(len(content)) > p.MaxTotalBytes {
			return filepath.SkipDir
		}

		entries = append(entries, FileEntry{
			Path:    rel,
			Content: string(content),
		})
		totalBytes += int64(len(content))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// PackageAsContext walks the workspace and returns a formatted context string suitable for appending to a prompt.
func (p *Packager) PackageAsContext() (string, error) {
	entries, err := p.Package()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("\n\n--- Workspace Context ---\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("\n### File: %s\n```\n%s\n```\n", e.Path, e.Content))
	}
	return b.String(), nil
}

func (p *Packager) isIncluded(rel string) bool {
	if len(p.IncludePatterns) == 0 {
		return true
	}
	for _, pat := range p.IncludePatterns {
		if matchGlob(pat, rel) {
			return true
		}
	}
	return false
}

func (p *Packager) isExcluded(rel string) bool {
	for _, pat := range p.ExcludePatterns {
		if matchGlob(pat, rel) {
			return true
		}
	}
	return false
}

// matchGlob performs a simplified glob match.
func matchGlob(pattern, name string) bool {
	pattern = strings.ToLower(pattern)
	name = strings.ToLower(name)
	// Handle directory/** patterns first (must precede bare * checks).
	if strings.HasSuffix(pattern, "/**") {
		prefix := pattern[:len(pattern)-3]
		return name == prefix || strings.HasPrefix(name, prefix+"/")
	}
	// Handle **/prefix patterns: match anywhere in path.
	if strings.HasPrefix(pattern, "**/") {
		suffix := pattern[3:]
		return strings.HasSuffix(name, suffix) || strings.Contains(name, "/"+suffix)
	}
	// Handle exact suffix match.
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(name, pattern[1:])
	}
	// Handle prefix match.
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, pattern[:len(pattern)-1])
	}
	return pattern == name
}
