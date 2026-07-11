package governance

import (
	"testing"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/audit"
)

func TestBuildAttentionQueueGolden(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	since := now.Add(-7 * 24 * time.Hour)

	tests := []struct {
		name  string
		in    InsightInputs
		want  []AttentionItem
		total int
	}{
		{
			name: "empty inputs yield empty queue",
			in:   InsightInputs{Now: now, WindowSince: since},
			want: nil,
		},
		{
			name: "policy denial without secret reason is high",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				PolicyDecisions: []PolicyDecisionRecord{
					{DecisionID: "pol_1", SessionID: "sess_1", Allowed: false, Reason: "classification blocked", RecordedAt: now.Add(-time.Hour)},
				},
			},
			want: []AttentionItem{
				{Key: "deny:sess_1:pol_1", Severity: "high", Category: "policy_deny", SessionID: "sess_1", Title: "Policy denial", Reason: "classification blocked", ObservedAt: now.Add(-time.Hour)},
			},
		},
		{
			name: "secret detection denial is critical",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				PolicyDecisions: []PolicyDecisionRecord{
					{DecisionID: "pol_2", SessionID: "sess_2", Allowed: false, Reason: "secret detected", RecordedAt: now.Add(-time.Hour)},
				},
			},
			want: []AttentionItem{
				{Key: "deny:sess_2:pol_2", Severity: "critical", Category: "policy_deny", SessionID: "sess_2", Title: "Policy denial", Reason: "secret detected", ObservedAt: now.Add(-time.Hour)},
			},
		},
		{
			name: "denial without session id falls back to correlation id",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				PolicyDecisions: []PolicyDecisionRecord{
					{DecisionID: "pol_3", CorrelationID: "corr_3", Allowed: false, Reason: "mcp tool denied", RecordedAt: now.Add(-time.Hour)},
				},
			},
			want: []AttentionItem{
				{Key: "deny:corr_3:pol_3", Severity: "high", Category: "policy_deny", Title: "Policy denial", Reason: "mcp tool denied", ObservedAt: now.Add(-time.Hour)},
			},
		},
		{
			name: "cost cap denial produces both a deny item and a costcap item",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				PolicyDecisions: []PolicyDecisionRecord{
					{DecisionID: "pol_4", SessionID: "sess_4", Allowed: false, Reason: "cost cap exceeded", EstimatedCostUSD: 12.5, CostCapUSD: 10, RecordedAt: now.Add(-time.Hour)},
				},
			},
			// Both items share severity "high" and the same ObservedAt, so the
			// final deterministic tiebreak (Key ascending) decides order:
			// "costcap:..." sorts before "deny:...".
			want: []AttentionItem{
				{Key: "costcap:sess_4", Severity: "high", Category: "cost_cap", SessionID: "sess_4", Title: "Cost cap exceeded", Reason: "estimated $12.50 exceeds cap $10.00", ObservedAt: now.Add(-time.Hour)},
				{Key: "deny:sess_4:pol_4", Severity: "high", Category: "policy_deny", SessionID: "sess_4", Title: "Policy denial", Reason: "cost cap exceeded", ObservedAt: now.Add(-time.Hour)},
			},
		},
		{
			name: "cost cap sourced from a session.denied audit event dedupes with the decision item",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				EventsBySession: map[string][]audit.Event{
					"sess_5": {
						{EventType: "session.denied", SessionID: "sess_5", Reason: "cost cap exceeded", EstimatedCostUSD: 20, CostCapUSD: 15, RecordedAt: now.Add(-2 * time.Hour)},
					},
				},
			},
			want: []AttentionItem{
				{Key: "costcap:sess_5", Severity: "high", Category: "cost_cap", SessionID: "sess_5", Title: "Cost cap exceeded", Reason: "estimated $20.00 exceeds cap $15.00", ObservedAt: now.Add(-2 * time.Hour)},
			},
		},
		{
			name: "running session with no recent activity is stalled",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				Sessions: []SessionRecord{
					{SessionID: "sess_6", Status: "running", CreatedAt: now.Add(-2 * time.Hour)},
				},
				EventsBySession: map[string][]audit.Event{
					"sess_6": {{EventType: "session.created", SessionID: "sess_6", RecordedAt: now.Add(-90 * time.Minute)}},
				},
			},
			want: []AttentionItem{
				{Key: "stalled:sess_6", Severity: "medium", Category: "stalled_session", SessionID: "sess_6", Title: "Session stalled", Reason: "status running with no audit activity since " + now.Add(-90*time.Minute).UTC().Format(time.RFC3339), ObservedAt: now.Add(-90 * time.Minute)},
			},
		},
		{
			name: "running session with recent activity is not stalled",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				Sessions: []SessionRecord{
					{SessionID: "sess_7", Status: "running", CreatedAt: now.Add(-2 * time.Hour)},
				},
				EventsBySession: map[string][]audit.Event{
					"sess_7": {{EventType: "session.created", SessionID: "sess_7", RecordedAt: now.Add(-5 * time.Minute)}},
				},
			},
			want: nil,
		},
		{
			name: "done session with no evidence is flagged",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				Sessions: []SessionRecord{
					{SessionID: "sess_8", Status: "done", CreatedAt: now.Add(-3 * time.Hour)},
				},
			},
			want: []AttentionItem{
				{Key: "noevidence:sess_8", Severity: "medium", Category: "missing_evidence", SessionID: "sess_8", Title: "No evidence recorded", Reason: "session done with zero evidence records", ObservedAt: now.Add(-3 * time.Hour)},
			},
		},
		{
			name: "done session with evidence is not flagged",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				Sessions: []SessionRecord{
					{SessionID: "sess_9", Status: "done", CreatedAt: now.Add(-3 * time.Hour)},
				},
				Evidence: []EvidenceRecord{{ID: "ev_1", SessionID: "sess_9", Status: "confirmed"}},
			},
			want: nil,
		},
		{
			name: "unavailable cost source is flagged low severity",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				Sessions: []SessionRecord{
					{SessionID: "sess_10", Status: "done", CreatedAt: now.Add(-4 * time.Hour)},
				},
				Evidence: []EvidenceRecord{{ID: "ev_2", SessionID: "sess_10", Status: "confirmed"}},
				UsageBySession: map[string]SessionUsageSummary{
					"sess_10": {CostSource: "unavailable"},
				},
			},
			want: []AttentionItem{
				{Key: "costunknown:sess_10", Severity: "low", Category: "cost_unknown", SessionID: "sess_10", Title: "Cost unknown", Reason: "cost source unavailable for session usage", ObservedAt: now.Add(-4 * time.Hour)},
			},
		},
		{
			name: "active kill switch is current state, critical severity",
			in: InsightInputs{
				Now:          now,
				WindowSince:  since,
				KillSwitches: map[string][]string{"agent": {"code-review"}},
			},
			want: []AttentionItem{
				{Key: "killswitch:agent:code-review", Severity: "critical", Category: "kill_switch", Title: "Kill switch active", Reason: "scope agent target code-review is blocked", ObservedAt: now},
			},
		},
		{
			name: "results sort by severity rank then observed time descending",
			in: InsightInputs{
				Now:         now,
				WindowSince: since,
				PolicyDecisions: []PolicyDecisionRecord{
					{DecisionID: "pol_low_first", SessionID: "sess_low", Allowed: false, Reason: "classification blocked", RecordedAt: now.Add(-3 * time.Hour)},
					{DecisionID: "pol_low_second", SessionID: "sess_low2", Allowed: false, Reason: "classification blocked", RecordedAt: now.Add(-time.Hour)},
					{DecisionID: "pol_critical", SessionID: "sess_crit", Allowed: false, Reason: "secret detected", RecordedAt: now.Add(-2 * time.Hour)},
				},
				KillSwitches: map[string][]string{"global": {"sessions"}},
			},
			// Within severity "critical", the newer ObservedAt (killswitch,
			// pinned to "now") sorts ahead of the older denial.
			want: []AttentionItem{
				{Key: "killswitch:global:sessions", Severity: "critical", Category: "kill_switch", Title: "Kill switch active", Reason: "scope global target sessions is blocked", ObservedAt: now},
				{Key: "deny:sess_crit:pol_critical", Severity: "critical", Category: "policy_deny", SessionID: "sess_crit", Title: "Policy denial", Reason: "secret detected", ObservedAt: now.Add(-2 * time.Hour)},
				{Key: "deny:sess_low2:pol_low_second", Severity: "high", Category: "policy_deny", SessionID: "sess_low2", Title: "Policy denial", Reason: "classification blocked", ObservedAt: now.Add(-time.Hour)},
				{Key: "deny:sess_low:pol_low_first", Severity: "high", Category: "policy_deny", SessionID: "sess_low", Title: "Policy denial", Reason: "classification blocked", ObservedAt: now.Add(-3 * time.Hour)},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildAttentionQueue(tc.in)
			if len(got.Items) != len(tc.want) {
				t.Fatalf("item count = %d, want %d: %#v", len(got.Items), len(tc.want), got.Items)
			}
			for i := range tc.want {
				if got.Items[i] != tc.want[i] {
					t.Fatalf("item[%d] = %#v, want %#v", i, got.Items[i], tc.want[i])
				}
			}
			if got.Summary.Total != len(tc.want) {
				t.Fatalf("summary total = %d, want %d", got.Summary.Total, len(tc.want))
			}
			if !got.Window.Since.Equal(since) || !got.Window.Until.Equal(now) {
				t.Fatalf("window = %#v, want since=%v until=%v", got.Window, since, now)
			}
		})
	}
}

func TestBuildAttentionQueueDeterministicAcrossRuns(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	in := InsightInputs{
		Now:         now,
		WindowSince: now.Add(-7 * 24 * time.Hour),
		Sessions: []SessionRecord{
			{SessionID: "sess_a", Status: "running", CreatedAt: now.Add(-2 * time.Hour)},
			{SessionID: "sess_b", Status: "done", CreatedAt: now.Add(-3 * time.Hour)},
		},
		PolicyDecisions: []PolicyDecisionRecord{
			{DecisionID: "pol_1", SessionID: "sess_a", Allowed: false, Reason: "secret detected", RecordedAt: now.Add(-time.Hour)},
		},
		KillSwitches: map[string][]string{"agent": {"a1", "a2"}, "global": {"sessions"}},
	}

	first := BuildAttentionQueue(in)
	for i := 0; i < 20; i++ {
		got := BuildAttentionQueue(in)
		if len(got.Items) != len(first.Items) {
			t.Fatalf("run %d: item count changed: %d vs %d", i, len(got.Items), len(first.Items))
		}
		for j := range first.Items {
			if got.Items[j] != first.Items[j] {
				t.Fatalf("run %d: item[%d] changed: %#v vs %#v", i, j, got.Items[j], first.Items[j])
			}
		}
	}
}

func TestBuildAttentionQueueSummaryCounts(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	in := InsightInputs{
		Now:         now,
		WindowSince: now.Add(-7 * 24 * time.Hour),
		PolicyDecisions: []PolicyDecisionRecord{
			{DecisionID: "pol_1", SessionID: "sess_1", Allowed: false, Reason: "secret detected", RecordedAt: now.Add(-time.Hour)},
			{DecisionID: "pol_2", SessionID: "sess_2", Allowed: false, Reason: "classification blocked", RecordedAt: now.Add(-time.Hour)},
		},
		KillSwitches: map[string][]string{"global": {"sessions"}},
	}
	got := BuildAttentionQueue(in)
	if got.Summary.Total != 3 {
		t.Fatalf("total = %d, want 3", got.Summary.Total)
	}
	if got.Summary.BySeverity["critical"] != 2 {
		t.Fatalf("critical count = %d, want 2", got.Summary.BySeverity["critical"])
	}
	if got.Summary.BySeverity["high"] != 1 {
		t.Fatalf("high count = %d, want 1", got.Summary.BySeverity["high"])
	}
	if got.Summary.ByCategory["policy_deny"] != 2 {
		t.Fatalf("policy_deny count = %d, want 2", got.Summary.ByCategory["policy_deny"])
	}
	if got.Summary.ByCategory["kill_switch"] != 1 {
		t.Fatalf("kill_switch count = %d, want 1", got.Summary.ByCategory["kill_switch"])
	}
}
