package contextresolver

import (
	"regexp"
	"strings"
)

// branchPatterns maps source systems to regexes that extract work-item IDs.
// The first capture group must be the work-item ID.
var branchPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	// Azure DevOps: users/name/ADO-456-x (ADO convention)
	{"ado", regexp.MustCompile(`(?i)(?:users/[^/]+/)([A-Z]+-\d+)(?:-|$)`)},
	// Jira: feature/ABC-123-description or bugfix/ABC-123
	{"jira", regexp.MustCompile(`(?i)(?:^|/)([A-Z]+-\d+)(?:-|$)`)},
	// GitHub/plain numeric: bugfix/123-x or fix/456
	{"github", regexp.MustCompile(`(?:^|/)(\d+)(?:-|$)`)},
}

// workTypePrefixes maps branch prefixes to inferred work types.
var workTypePrefixes = map[string]string{
	"feature":  "feature",
	"feat":     "feature",
	"frontend": "frontend",
	"ui":       "frontend",
	"backend":  "backend",
	"api":      "backend",
	"bugfix":   "bugfix",
	"fix":      "bugfix",
	"hotfix":   "bugfix",
	"docs":     "docs",
	"doc":      "docs",
	"refactor": "refactor",
	"test":     "test",
	"tests":    "test",
	"security": "security",
	"sec":      "security",
	"chore":    "chore",
	"release":  "release",
}

// ParseBranch extracts work-item ID, work-item type and source system from a
// Git branch name. It never returns an error; missing data yields empty strings.
func ParseBranch(branch string) (workItemID, workItemType, sourceSystem string) {
	if branch == "" {
		return "", "", ""
	}

	parts := strings.Split(branch, "/")
	if len(parts) >= 2 {
		workItemType = inferWorkType(parts[0])
	}

	for _, p := range branchPatterns {
		matches := p.pattern.FindStringSubmatch(branch)
		if len(matches) >= 2 && matches[1] != "" {
			workItemID = matches[1]
			sourceSystem = p.name
			break
		}
	}

	// If no prefix matched but a work item ID was found, default to feature.
	if workItemType == "" && workItemID != "" {
		workItemType = "feature"
	}

	return workItemID, workItemType, sourceSystem
}

func inferWorkType(prefix string) string {
	if t, ok := workTypePrefixes[strings.ToLower(prefix)]; ok {
		return t
	}
	return ""
}
