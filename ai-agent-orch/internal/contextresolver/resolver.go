// Package contextresolver extracts session context from git state, branch names, and
// local environment without treating any of it as authoritative identity.
package contextresolver

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SessionContext holds non-authoritative metadata about the developer's local
// working state. The authoritative actor remains the OIDC or dev-token subject.
type SessionContext struct {
	RepoURL      string `json:"repo_url,omitempty"`
	Branch       string `json:"branch,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	WorkItemID   string `json:"work_item_id,omitempty"`
	WorkItemType string `json:"work_item_type,omitempty"` // feature, bugfix, docs, etc.
	ActorHint    string `json:"actor_hint,omitempty"`     // git user/email or OS username
	SourceSystem string `json:"source_system,omitempty"`  // jira, ado, github, unknown
}

// Resolver extracts session context from the local environment.
type Resolver struct {
	workDir string
}

// New creates a Resolver that inspects the given working directory.
// An empty workDir defaults to os.Getwd().
func New(workDir string) *Resolver {
	if workDir == "" {
		wd, _ := os.Getwd()
		workDir = wd
	}
	return &Resolver{workDir: workDir}
}

// Resolve returns the best-effort session context. No field is ever guaranteed;
// the resolver fails open and returns what it can find.
func (r *Resolver) Resolve() SessionContext {
	ctx := SessionContext{}

	// Git-derived metadata.
	if r.isGitRepo() {
		ctx.RepoURL = r.gitRemoteURL()
		ctx.Branch = r.gitBranch()
		ctx.CommitSHA = r.gitCommitSHA()
	}

	// Branch parsing for work item extraction.
	if ctx.Branch != "" {
		ctx.WorkItemID, ctx.WorkItemType, ctx.SourceSystem = ParseBranch(ctx.Branch)
	}

	// Non-authoritative actor hint.
	ctx.ActorHint = r.actorHint()

	return ctx
}

func (r *Resolver) isGitRepo() bool {
	_, err := os.Stat(filepath.Join(r.workDir, ".git"))
	return err == nil
}

func (r *Resolver) gitRemoteURL() string {
	out, _ := r.git("remote", "get-url", "origin")
	return SanitizeRepositoryURL(out)
}

// SanitizeRepositoryURL strips URL userinfo before repo metadata is persisted or
// sent to the governance shell. SSH-style remotes are returned unchanged.
func SanitizeRepositoryURL(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

func (r *Resolver) gitBranch() string {
	out, _ := r.git("branch", "--show-current")
	return strings.TrimSpace(out)
}

func (r *Resolver) gitCommitSHA() string {
	out, _ := r.git("rev-parse", "HEAD")
	return strings.TrimSpace(out)
}

func (r *Resolver) actorHint() string {
	name, _ := r.git("config", "user.name")
	email, _ := r.git("config", "user.email")
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name != "" && email != "" {
		return fmt.Sprintf("%s <%s>", name, email)
	}
	if name != "" {
		return name
	}
	if email != "" {
		return email
	}
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	if user := os.Getenv("USERNAME"); user != "" {
		return user
	}
	return ""
}

func (r *Resolver) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.workDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
