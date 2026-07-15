package governance

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

// AttentionItem is one entry in the deterministic governance attention queue.
// Every item is folded from data already stored elsewhere (policy decisions,
// audit events, evidence, session records, kill switches); the queue itself
// stores nothing new.
type AttentionItem struct {
	Key        string    `json:"key"`
	Severity   string    `json:"severity"`
	Category   string    `json:"category"`
	SessionID  string    `json:"session_id,omitempty"`
	Title      string    `json:"title"`
	Reason     string    `json:"reason"`
	ObservedAt time.Time `json:"observed_at"`
}

// InsightSummary reports item counts by severity and category so a caller
// can render a dashboard without re-scanning the item list.
type InsightSummary struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	ByCategory map[string]int `json:"by_category"`
}

// InsightWindow records the bounded time range a projection was built over.
type InsightWindow struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// InsightResult is the full governance attention queue projection.
type InsightResult struct {
	Window  InsightWindow   `json:"window"`
	Items   []AttentionItem `json:"items"`
	Summary InsightSummary  `json:"summary"`
}

// defaultStalledAfter is how long a running/confirming session can go
// without a new audit event before it is flagged as stalled.
const defaultStalledAfter = 30 * time.Minute

// insightSessionCap bounds how many of the newest in-window sessions feed
// the projection, so a single request stays cheap regardless of history size.
const insightSessionCap = 500

// InsightInputs are the bounded, already-fetched facts BuildAttentionQueue
// folds into an attention queue. Every field is read-only data the caller
// gathered beforehand; BuildAttentionQueue performs no I/O of its own.
type InsightInputs struct {
	// Now anchors "current state" items (like active kill switches) and the
	// stalled-session check. Callers should pass a fixed clock reading so a
	// single request is internally consistent.
	Now time.Time
	// WindowSince is the start of the bounded time window used to gather
	// Sessions, echoed back on the result for the caller's response.
	WindowSince time.Time
	// Sessions are the in-window session records to evaluate, newest first.
	Sessions []SessionRecord
	// EventsBySession holds each session's audit trail, keyed by session ID.
	EventsBySession map[string][]audit.Event
	// UsageBySession holds each session's usage summary (as computed by
	// SummarizeSessionUsageWithPricing), keyed by session ID.
	UsageBySession map[string]SessionUsageSummary
	// Evidence holds evidence records already scoped to the sessions above.
	Evidence []EvidenceRecord
	// PolicyDecisions holds policy_decisions rows already scoped to the
	// window (and, for actor-scoped callers, to the requesting actor).
	PolicyDecisions []PolicyDecisionRecord
	// KillSwitches is the current kill-switch state: scope -> blocked IDs.
	// There is no kill-switch history table, so active switches are surfaced
	// as current-state items rather than reconstructed from audit.
	KillSwitches map[string][]string
	// StalledAfter overrides the stalled-session threshold. Zero uses
	// defaultStalledAfter.
	StalledAfter time.Duration
}

// BuildAttentionQueue folds bounded, already-fetched governance facts into a
// deterministic attention queue. It performs no I/O and no writes: it is a
// pure function of its inputs, safe to call from a read-only GET handler and
// safe to golden-test with synthetic data.
func BuildAttentionQueue(in InsightInputs) InsightResult {
	stalledAfter := in.StalledAfter
	if stalledAfter <= 0 {
		stalledAfter = defaultStalledAfter
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	items := make(map[string]AttentionItem)
	addItem := func(item AttentionItem) {
		if item.Key == "" {
			return
		}
		existing, ok := items[item.Key]
		if !ok || item.ObservedAt.After(existing.ObservedAt) {
			items[item.Key] = item
		}
	}

	// deny:<session_or_correlation>:<decision_id> -- policy_decisions rows
	// that were not allowed. Secret-detection denials are critical; every
	// other denial is high.
	for _, decision := range in.PolicyDecisions {
		if decision.Allowed {
			continue
		}
		subject := decision.SessionID
		if subject == "" {
			subject = decision.CorrelationID
		}
		severity := "high"
		if strings.Contains(strings.ToLower(decision.Reason), "secret") {
			severity = "critical"
		}
		addItem(AttentionItem{
			Key:        fmt.Sprintf("deny:%s:%s", subject, decision.DecisionID),
			Severity:   severity,
			Category:   "policy_deny",
			SessionID:  decision.SessionID,
			Title:      "Policy denial",
			Reason:     decision.Reason,
			ObservedAt: decision.RecordedAt,
		})
		if isCostCapReason(decision.Reason) {
			addItem(AttentionItem{
				Key:        fmt.Sprintf("costcap:%s", subject),
				Severity:   "high",
				Category:   "cost_cap",
				SessionID:  decision.SessionID,
				Title:      "Cost cap exceeded",
				Reason:     costCapReason(decision.EstimatedCostUSD, decision.CostCapUSD),
				ObservedAt: decision.RecordedAt,
			})
		}
	}

	// costcap:<session> -- session.denied audit events carrying cost-cap
	// fields. Naturally dedupes against a decision-sourced item for the same
	// session via the shared "costcap:<session>" key.
	for sessionID, events := range in.EventsBySession {
		for _, event := range events {
			if event.EventType != "session.denied" {
				continue
			}
			if !isCostCapReason(event.Reason) {
				continue
			}
			addItem(AttentionItem{
				Key:        fmt.Sprintf("costcap:%s", sessionID),
				Severity:   "high",
				Category:   "cost_cap",
				SessionID:  sessionID,
				Title:      "Cost cap exceeded",
				Reason:     costCapReason(event.EstimatedCostUSD, event.CostCapUSD),
				ObservedAt: event.RecordedAt,
			})
		}
	}

	evidenceCountBySession := make(map[string]int, len(in.Evidence))
	for _, record := range in.Evidence {
		evidenceCountBySession[record.SessionID]++
	}

	for _, session := range in.Sessions {
		latest := latestEventTime(in.EventsBySession[session.SessionID], session.CreatedAt)

		// stalled:<session> -- running/confirming sessions gone quiet.
		if (session.Status == "running" || session.Status == "confirming") && now.Sub(latest) >= stalledAfter {
			addItem(AttentionItem{
				Key:        fmt.Sprintf("stalled:%s", session.SessionID),
				Severity:   "medium",
				Category:   "stalled_session",
				SessionID:  session.SessionID,
				Title:      "Session stalled",
				Reason:     fmt.Sprintf("status %s with no audit activity since %s", session.Status, latest.UTC().Format(time.RFC3339)),
				ObservedAt: latest,
			})
		}

		// noevidence:<session> -- done/failed sessions with zero evidence.
		if (session.Status == "done" || session.Status == "failed") && evidenceCountBySession[session.SessionID] == 0 {
			addItem(AttentionItem{
				Key:        fmt.Sprintf("noevidence:%s", session.SessionID),
				Severity:   "medium",
				Category:   "missing_evidence",
				SessionID:  session.SessionID,
				Title:      "No evidence recorded",
				Reason:     fmt.Sprintf("session %s with zero evidence records", session.Status),
				ObservedAt: latest,
			})
		}

		// costunknown:<session> -- usage summary could not attribute a cost source.
		if usage, ok := in.UsageBySession[session.SessionID]; ok && usage.CostSource == "unavailable" {
			addItem(AttentionItem{
				Key:        fmt.Sprintf("costunknown:%s", session.SessionID),
				Severity:   "low",
				Category:   "cost_unknown",
				SessionID:  session.SessionID,
				Title:      "Cost unknown",
				Reason:     "cost source unavailable for session usage",
				ObservedAt: latest,
			})
		}
	}

	// killswitch:<scope>:<target> -- current-state kill switches. There is
	// no kill-switch history table, so these are surfaced as of "now" rather
	// than reconstructed from audit.
	for scope, ids := range in.KillSwitches {
		for _, id := range ids {
			addItem(AttentionItem{
				Key:        fmt.Sprintf("killswitch:%s:%s", scope, id),
				Severity:   "critical",
				Category:   "kill_switch",
				Title:      "Kill switch active",
				Reason:     fmt.Sprintf("scope %s target %s is blocked", scope, id),
				ObservedAt: now,
			})
		}
	}

	result := make([]AttentionItem, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		ri, rj := severityRank(result[i].Severity), severityRank(result[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if !result[i].ObservedAt.Equal(result[j].ObservedAt) {
			return result[i].ObservedAt.After(result[j].ObservedAt)
		}
		return result[i].Key < result[j].Key
	})

	summary := InsightSummary{Total: len(result), BySeverity: map[string]int{}, ByCategory: map[string]int{}}
	for _, item := range result {
		summary.BySeverity[item.Severity]++
		summary.ByCategory[item.Category]++
	}

	return InsightResult{
		Window:  InsightWindow{Since: in.WindowSince, Until: now},
		Items:   result,
		Summary: summary,
	}
}

func isCostCapReason(reason string) bool {
	return strings.Contains(strings.ToLower(reason), "cost cap")
}

func costCapReason(estimatedCostUSD, costCapUSD float64) string {
	return fmt.Sprintf("estimated $%.2f exceeds cap $%.2f", estimatedCostUSD, costCapUSD)
}

func latestEventTime(events []audit.Event, fallback time.Time) time.Time {
	latest := fallback
	for _, event := range events {
		if event.RecordedAt.After(latest) {
			latest = event.RecordedAt
		}
	}
	return latest
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}
