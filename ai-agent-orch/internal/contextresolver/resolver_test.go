package contextresolver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBranchJira(t *testing.T) {
	id, typ, sys := ParseBranch("feature/ABC-123-description")
	if id != "ABC-123" {
		t.Fatalf("expected work item ABC-123, got %q", id)
	}
	if typ != "feature" {
		t.Fatalf("expected type feature, got %q", typ)
	}
	if sys != "jira" {
		t.Fatalf("expected source jira, got %q", sys)
	}
}

func TestParseBranchADO(t *testing.T) {
	id, typ, sys := ParseBranch("users/name/ADO-456-x")
	if id != "ADO-456" {
		t.Fatalf("expected work item ADO-456, got %q", id)
	}
	if typ != "feature" {
		t.Fatalf("expected type feature, got %q", typ)
	}
	if sys != "ado" {
		t.Fatalf("expected source ado, got %q", sys)
	}
}

func TestParseBranchGitHubNumeric(t *testing.T) {
	id, typ, sys := ParseBranch("bugfix/123-x")
	if id != "123" {
		t.Fatalf("expected work item 123, got %q", id)
	}
	if typ != "bugfix" {
		t.Fatalf("expected type bugfix, got %q", typ)
	}
	if sys != "github" {
		t.Fatalf("expected source github, got %q", sys)
	}
}

func TestParseBranchNoMatch(t *testing.T) {
	id, typ, sys := ParseBranch("main")
	if id != "" || typ != "" || sys != "" {
		t.Fatalf("expected empty for main branch, got %q %q %q", id, typ, sys)
	}
}

func TestParseBranchRefactor(t *testing.T) {
	id, typ, sys := ParseBranch("refactor/cleanup-auth")
	if id != "" {
		t.Fatalf("expected no work item, got %q", id)
	}
	if typ != "refactor" {
		t.Fatalf("expected type refactor, got %q", typ)
	}
	_ = sys
}

func TestParseBranchFrontendAndBackendTypes(t *testing.T) {
	cases := []struct {
		branch string
		want   string
	}{
		{branch: "frontend/APP-123-navigation", want: "frontend"},
		{branch: "ui/APP-123-navigation", want: "frontend"},
		{branch: "backend/APP-124-api", want: "backend"},
		{branch: "api/APP-124-api", want: "backend"},
	}
	for _, tc := range cases {
		t.Run(tc.branch, func(t *testing.T) {
			_, typ, _ := ParseBranch(tc.branch)
			if typ != tc.want {
				t.Fatalf("expected type %q, got %q", tc.want, typ)
			}
		})
	}
}

func TestParseBranchSecurity(t *testing.T) {
	id, typ, _ := ParseBranch("security/SEC-789-patch")
	if id != "SEC-789" {
		t.Fatalf("expected work item SEC-789, got %q", id)
	}
	if typ != "security" {
		t.Fatalf("expected type security, got %q", typ)
	}
}

func TestParseBranchEmpty(t *testing.T) {
	id, typ, sys := ParseBranch("")
	if id != "" || typ != "" || sys != "" {
		t.Fatalf("expected empty for empty branch")
	}
}

func TestResolverInGitRepo(t *testing.T) {
	// Create a temporary git repository.
	tmpDir := t.TempDir()
	mustGit(t, tmpDir, "init")
	mustGit(t, tmpDir, "config", "user.name", "Test User")
	mustGit(t, tmpDir, "config", "user.email", "test@example.com")
	mustWrite(t, filepath.Join(tmpDir, "a.txt"), "a")
	mustGit(t, tmpDir, "add", ".")
	mustGit(t, tmpDir, "commit", "-m", "initial")
	mustGit(t, tmpDir, "checkout", "-b", "feature/PROJ-42-auth")
	mustGit(t, tmpDir, "remote", "add", "origin", "https://github.com/acme/rocket.git")

	r := New(tmpDir)
	ctx := r.Resolve()

	if !strings.Contains(ctx.RepoURL, "github.com/acme/rocket") {
		t.Fatalf("expected repo url, got %q", ctx.RepoURL)
	}
	if ctx.Branch != "feature/PROJ-42-auth" {
		t.Fatalf("expected branch, got %q", ctx.Branch)
	}
	if len(ctx.CommitSHA) != 40 {
		t.Fatalf("expected 40-char sha, got %q", ctx.CommitSHA)
	}
	if ctx.WorkItemID != "PROJ-42" {
		t.Fatalf("expected work item PROJ-42, got %q", ctx.WorkItemID)
	}
	if ctx.WorkItemType != "feature" {
		t.Fatalf("expected type feature, got %q", ctx.WorkItemType)
	}
	if ctx.SourceSystem != "jira" {
		t.Fatalf("expected source jira, got %q", ctx.SourceSystem)
	}
	if ctx.ActorHint != "Test User <test@example.com>" {
		t.Fatalf("expected actor hint, got %q", ctx.ActorHint)
	}
}

func TestResolverSanitisesCredentialedRemoteURL(t *testing.T) {
	tmpDir := t.TempDir()
	mustGit(t, tmpDir, "init")
	mustGit(t, tmpDir, "config", "user.name", "Test User")
	mustGit(t, tmpDir, "config", "user.email", "test@example.com")
	mustWrite(t, filepath.Join(tmpDir, "a.txt"), "a")
	mustGit(t, tmpDir, "add", ".")
	mustGit(t, tmpDir, "commit", "-m", "initial")
	mustGit(t, tmpDir, "remote", "add", "origin", "https://user:pass@example.com/org/repo.git")

	ctx := New(tmpDir).Resolve()

	if ctx.RepoURL != "https://example.com/org/repo.git" {
		t.Fatalf("expected sanitised repo URL, got %q", ctx.RepoURL)
	}
	if strings.Contains(ctx.RepoURL, "user") || strings.Contains(ctx.RepoURL, "pass") {
		t.Fatalf("repo URL leaked credentials: %q", ctx.RepoURL)
	}
}

func TestResolverOutsideGitRepo(t *testing.T) {
	tmpDir := t.TempDir()
	r := New(tmpDir)
	ctx := r.Resolve()
	if ctx.RepoURL != "" || ctx.Branch != "" || ctx.CommitSHA != "" {
		t.Fatal("expected empty git fields outside repo")
	}
}

func TestResolverPrefersExplicitWorkDir(t *testing.T) {
	// New with empty string should fall back to os.Getwd.
	r := New("")
	if r.workDir == "" {
		t.Fatal("expected fallback workDir")
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
