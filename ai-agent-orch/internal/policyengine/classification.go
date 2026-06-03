package policyengine

import (
	"fmt"
	"regexp"
	"strings"
)

var classificationRank = map[string]int{
	"public":       0,
	"internal":     1,
	"confidential": 2,
	"restricted":   3,
}

type secretPattern struct {
	name string
	re   *regexp.Regexp
}

// SecretPatterns is the best-effort secret detection regex list.
// It is a backstop, not a primary control. Secret scanners (TruffleHog, GitLeaks)
// and pre-commit hooks should be the first line of defense.
var SecretPatterns = []secretPattern{
	{name: "openrouter_api_key", re: regexp.MustCompile(`(?i)\b(?:OPENROUTER_API_KEY\s*=\s*)?sk-or-v1-[A-Za-z0-9_-]{10,}`)},
	{name: "openai_api_key", re: regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`)},
	{name: "aws_access_key_id", re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{name: "private_key", re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{name: "password_assignment", re: regexp.MustCompile(`(?i)\b(password|passwd|pwd)\s*[:=]\s*[^[:space:]]{8,}`)},
}

func classificationExceedsMax(classification string, max string) (bool, error) {
	classification = strings.ToLower(strings.TrimSpace(classification))
	max = strings.ToLower(strings.TrimSpace(max))
	if max == "" {
		max = "internal"
	}
	classificationValue, ok := classificationRank[classification]
	if !ok {
		return false, fmt.Errorf("unknown classification %q", classification)
	}
	maxValue, ok := classificationRank[max]
	if !ok {
		return false, fmt.Errorf("unknown max classification %q", max)
	}
	return classificationValue > maxValue, nil
}

func DetectSecrets(text string) []string {
	seen := map[string]struct{}{}
	var findings []string
	for _, pattern := range SecretPatterns {
		if pattern.re.MatchString(text) {
			if _, ok := seen[pattern.name]; ok {
				continue
			}
			seen[pattern.name] = struct{}{}
			findings = append(findings, pattern.name)
		}
	}
	return findings
}
