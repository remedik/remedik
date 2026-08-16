package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

func TestPosturePanel(t *testing.T) {
	tests := []struct {
		name         string
		posture      Posture
		wantHeadline string
		wantTone     string
		wantIn       string
	}{
		{
			name:         "dry-run everywhere",
			posture:      Posture{DryRun: true},
			wantHeadline: "Dry-run",
			wantTone:     toneDryRun,
			wantIn:       "Nothing in the cluster is changed",
		},
		{
			name:         "live everywhere",
			posture:      Posture{},
			wantHeadline: "Live",
			wantTone:     toneOK,
			wantIn:       "remediated",
		},
		{
			// The misreading this panel exists to prevent: "dryRun: true" in
			// the values file over a cluster that acts in one namespace.
			name:         "reporting by default, acting somewhere",
			posture:      Posture{DryRun: true, Live: []string{"staging"}},
			wantHeadline: "Mixed",
			wantTone:     toneRunning,
			wantIn:       "except in staging, where remedik acts",
		},
		{
			name:         "acting by default, reporting somewhere",
			posture:      Posture{DryRunOnly: []string{"prod"}},
			wantHeadline: "Mixed",
			wantTone:     toneRunning,
			wantIn:       "except in prod, where remedik only reports",
		},
		{
			name:         "several namespaces are listed readably",
			posture:      Posture{DryRun: true, Live: []string{"dev", "staging", "test"}},
			wantHeadline: "Mixed",
			wantTone:     toneRunning,
			wantIn:       "dev, staging and test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			panel := buildPosturePanel(tt.posture)

			if panel.Headline != tt.wantHeadline {
				t.Errorf("Headline = %q, want %q", panel.Headline, tt.wantHeadline)
			}
			if panel.Tone != tt.wantTone {
				t.Errorf("Tone = %q, want %q", panel.Tone, tt.wantTone)
			}
			if !strings.Contains(panel.Detail, tt.wantIn) {
				t.Errorf("Detail = %q, want it to contain %q", panel.Detail, tt.wantIn)
			}
		})
	}
}

func TestAttentionPanel(t *testing.T) {
	failed := func(name string, escalation *v1alpha1.EscalationStatus, reason string) v1alpha1.Remediation {
		rem := failedRemediation(name, 10)
		rem.Status.Escalation = escalation
		if reason != "" {
			rem.Status.Reason = reason
		}
		return rem
	}
	sent := &v1alpha1.EscalationStatus{Phase: v1alpha1.StepPhaseSucceeded}
	lost := &v1alpha1.EscalationStatus{Phase: v1alpha1.StepPhaseFailed}

	t.Run("nothing failed", func(t *testing.T) {
		panel := buildAttention([]v1alpha1.Remediation{succeededRemediation("ok-1", 5)})
		if panel.Any() {
			t.Errorf("Any() = true with nothing failed: %+v", panel.Items)
		}
	})

	t.Run("a page nobody received leads", func(t *testing.T) {
		panel := buildAttention([]v1alpha1.Remediation{
			failed("bad-1", sent, ""),
			failed("bad-2", lost, ""),
			failed("bad-3", nil, ""),
		})

		if len(panel.Items) < 2 {
			t.Fatalf("items = %+v, want the failed escalation and the un-escalated failure", panel.Items)
		}
		// Ordered by how much silence each represents.
		if !strings.Contains(panel.Items[0].Label, "escalation") {
			t.Errorf("first item is %q; the failed page must lead", panel.Items[0].Label)
		}
		if panel.Items[0].Count != 1 {
			t.Errorf("failed escalations = %d, want 1", panel.Items[0].Count)
		}
		// The label must not repeat the count, which is shown beside it.
		if strings.HasPrefix(panel.Items[0].Label, "1 ") {
			t.Errorf("Label = %q; the count is rendered separately", panel.Items[0].Label)
		}
		for _, item := range panel.Items {
			if item.URL == "" {
				t.Errorf("item %q has no link to its evidence", item.Label)
			}
		}
	})

	t.Run("failures that were all reported still say so", func(t *testing.T) {
		panel := buildAttention([]v1alpha1.Remediation{failed("bad-1", sent, "")})

		if len(panel.Items) != 1 {
			t.Fatalf("items = %+v, want one", panel.Items)
		}
		if !strings.Contains(panel.Items[0].Detail, "somebody was told") {
			t.Errorf("Detail = %q, want it to say the escalation worked", panel.Items[0].Detail)
		}
	})

	t.Run("an interrupted execution is called out", func(t *testing.T) {
		panel := buildAttention([]v1alpha1.Remediation{
			failed("bad-1", sent, v1alpha1.ReasonInterrupted),
		})

		var found bool
		for _, item := range panel.Items {
			if strings.Contains(item.Label, "interrupted") {
				found = true
			}
		}
		if !found {
			t.Errorf("items = %+v, want one naming the interruption", panel.Items)
		}
	})
}

func TestActivityPanel(t *testing.T) {
	now := testNow()

	records := []v1alpha1.Remediation{
		succeededRemediation("ok-now", 5),     // this hour
		failedRemediation("bad-now", 20),      // this hour or the last
		succeededRemediation("ok-old", 60*30), // well outside the window
		simulatedRemediation("sim", "deployment/payments/api", 90),
	}

	panel := buildActivity(records, now)

	if len(panel.Bars) != activityHours {
		t.Fatalf("bars = %d, want %d", len(panel.Bars), activityHours)
	}
	if panel.Total != 3 {
		t.Errorf("Total = %d, want 3: the 30-hour-old record is outside the window", panel.Total)
	}
	if !panel.Any() {
		t.Error("Any() = false although three executions are in the window")
	}
	if panel.Busiest < 1 {
		t.Errorf("Busiest = %d, want at least 1", panel.Busiest)
	}

	// The last bar is the current hour, and bars read left to right in time.
	if panel.Bars[len(panel.Bars)-1].Label != now.Truncate(time.Hour).Format("15:04") {
		t.Errorf("the last bar is %q, want the current hour", panel.Bars[len(panel.Bars)-1].Label)
	}

	var counted int
	for _, bar := range panel.Bars {
		counted += bar.Total
		if bar.Total != bar.Succeeded+bar.Failed+bar.Simulated {
			t.Errorf("bar %s: total %d does not match its parts", bar.Label, bar.Total)
		}
		if bar.Percent < 0 || bar.Percent > 100 {
			t.Errorf("bar %s: percent %d out of range", bar.Label, bar.Percent)
		}
	}
	if counted != panel.Total {
		t.Errorf("the bars hold %d executions, the panel claims %d", counted, panel.Total)
	}
}

func TestActivityPanel_EmptyWindowDoesNotDivideByZero(t *testing.T) {
	panel := buildActivity(nil, testNow())

	if panel.Any() {
		t.Error("Any() = true with no records")
	}
	for _, bar := range panel.Bars {
		if bar.Percent != 0 {
			t.Errorf("bar %s: percent %d, want 0", bar.Label, bar.Percent)
		}
	}
}

func TestBreakdown(t *testing.T) {
	records := []v1alpha1.Remediation{
		succeededRemediation("a", 30),
		failedRemediation("b", 20),
		succeededRemediation("c", 10),
	}
	records[0].Spec.Target = "deployment/payments/api"
	records[1].Spec.Target = "deployment/payments/api"
	records[2].Spec.Target = "deployment/checkout/web"

	rows := buildBreakdown(records, targetNamespaceOf, byNamespace)

	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want two namespaces", rows)
	}
	if rows[0].Name != "payments" || rows[0].Total != 2 {
		t.Errorf("first row = %+v, want payments with 2 (busiest first)", rows[0])
	}
	if rows[0].Failed != 1 {
		t.Errorf("payments failed = %d, want 1", rows[0].Failed)
	}
	if rows[0].Share != 100 {
		t.Errorf("the busiest row's share = %d, want 100", rows[0].Share)
	}
	if rows[0].URL != "/remediations?namespace=payments" {
		t.Errorf("URL = %q, want the filtered list", rows[0].URL)
	}
	if rows[1].Share != 50 {
		t.Errorf("the second row's share = %d, want 50", rows[1].Share)
	}
}

// A node belongs to no namespace, so it contributes to the strategy
// breakdown and not the namespace one — rather than to a blank row.
func TestBreakdown_SkipsRecordsWithoutTheKey(t *testing.T) {
	records := []v1alpha1.Remediation{succeededRemediation("drain-1", 10)}
	records[0].Spec.Target = "node/worker-1"

	if rows := buildBreakdown(records, targetNamespaceOf, byNamespace); len(rows) != 0 {
		t.Errorf("namespace rows = %+v, want none", rows)
	}
	if rows := buildBreakdown(records, strategyOf, byStrategy); len(rows) != 1 {
		t.Errorf("strategy rows = %+v, want one", rows)
	}
}

// A panel that reshuffles under the reader is a panel nobody trusts.
func TestBreakdown_OrderIsStableForEqualCounts(t *testing.T) {
	records := []v1alpha1.Remediation{
		succeededRemediation("a", 30),
		succeededRemediation("b", 20),
	}
	records[0].Spec.Target = "deployment/zulu/api"
	records[1].Spec.Target = "deployment/alpha/api"

	rows := buildBreakdown(records, targetNamespaceOf, byNamespace)
	if len(rows) != 2 || rows[0].Name != "alpha" || rows[1].Name != "zulu" {
		t.Errorf("rows = %+v, want alphabetical when the counts tie", rows)
	}
}

// Every figure on the dashboard links to the list that explains it.
func TestOverviewStatsAllLink(t *testing.T) {
	view := buildOverview(
		[]v1alpha1.Remediation{succeededRemediation("ok-1", 5)},
		[]v1alpha1.RemediationStrategy{enabledStrategy()},
		Posture{},
		testNow(),
	)

	for _, stat := range view.Stats {
		if stat.URL == "" {
			t.Errorf("stat %q has no link", stat.Label)
		}
		if !strings.HasPrefix(stat.URL, remediationsPath) {
			t.Errorf("stat %q links to %q, want the list", stat.Label, stat.URL)
		}
	}
}

// The overview is a summary. A long table on it is the thing this rework
// removed, and the easiest thing to add back by accident.
func TestOverviewShowsOnlyATail(t *testing.T) {
	records := make([]v1alpha1.Remediation, 0, recentLimit*3)
	for i := range recentLimit * 3 {
		records = append(records, succeededRemediation("ok-"+string(rune('a'+i%26))+string(rune('a'+i/26)), i))
	}

	view := buildOverview(records, nil, Posture{}, testNow())

	if len(view.Recent) != recentLimit {
		t.Errorf("Recent = %d rows, want %d", len(view.Recent), recentLimit)
	}
	if view.Total != len(records) {
		t.Errorf("Total = %d, want every record counted", view.Total)
	}
}
