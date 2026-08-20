package dashboard

import (
	"net/url"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
		panel := buildAttention(ptrs([]v1alpha1.Remediation{succeededRemediation("ok-1", 5)}))
		if panel.Any() {
			t.Errorf("Any() = true with nothing failed: %+v", panel.Items)
		}
	})

	t.Run("a page nobody received leads", func(t *testing.T) {
		panel := buildAttention(ptrs([]v1alpha1.Remediation{
			failed("bad-1", sent, ""),
			failed("bad-2", lost, ""),
			failed("bad-3", nil, ""),
		}))

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
		panel := buildAttention(ptrs([]v1alpha1.Remediation{failed("bad-1", sent, "")}))

		if len(panel.Items) != 1 {
			t.Fatalf("items = %+v, want one", panel.Items)
		}
		if !strings.Contains(panel.Items[0].Detail, "somebody was told") {
			t.Errorf("Detail = %q, want it to say the escalation worked", panel.Items[0].Detail)
		}
	})

	t.Run("an interrupted execution is called out", func(t *testing.T) {
		panel := buildAttention(ptrs([]v1alpha1.Remediation{
			failed("bad-1", sent, v1alpha1.ReasonInterrupted),
		}))

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

	panel := buildActivity(ptrs(records), now)

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

	rows := buildBreakdown(ptrs(records), targetNamespaceOf, byNamespace)

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

	if rows := buildBreakdown(ptrs(records), targetNamespaceOf, byNamespace); len(rows) != 0 {
		t.Errorf("namespace rows = %+v, want none", rows)
	}
	if rows := buildBreakdown(ptrs(records), strategyOf, byStrategy); len(rows) != 1 {
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

	rows := buildBreakdown(ptrs(records), targetNamespaceOf, byNamespace)
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

// A queue nobody can see is a queue nobody empties, and an approval gate that
// silently accumulates is worse than none: it looks like remediation working.
func TestBuildAttention_WaitingForApprovalComesFirst(t *testing.T) {
	waiting := succeededRemediation("waiting", 5)
	waiting.Status.State = v1alpha1.RemediationStateAwaitingApproval
	waiting.Status.Message = "waiting for approval; 12m left before this escalates"

	// A failure nobody was told about, which is the loudest of the reports.
	untold := failedRemediation("untold", 4)
	untold.Status.Escalation = nil

	panel := buildAttention(ptrs([]v1alpha1.Remediation{untold, waiting}))

	if len(panel.Items) < 2 {
		t.Fatalf("items = %d, want the waiting record and the failure", len(panel.Items))
	}
	// First, because it is the only entry where somebody doing something
	// changes the outcome. The rest are reports; this is a request.
	if !strings.Contains(panel.Items[0].Label, "waiting for you") {
		t.Errorf("first item = %q, want the approval queue ahead of the reports",
			panel.Items[0].Label)
	}
	if !strings.Contains(panel.Items[0].Detail, "escalate") {
		t.Errorf("detail = %q, want it to say what happens if nobody looks",
			panel.Items[0].Detail)
	}
	// And it links to the queue, so the panel is a way in rather than a
	// notice. The queue rather than a filtered list: these records have a
	// deadline, and the page that orders them by it is the one that helps.
	if panel.Items[0].URL != approvalsPath {
		t.Errorf("URL = %q, want the approvals queue", panel.Items[0].URL)
	}
}

// A record waiting for a retry needs nobody; one waiting for a person needs
// somebody now. They must not look alike.
func TestStateTone_ApprovalDoesNotLookLikeARetry(t *testing.T) {
	waiting := stateTone(v1alpha1.RemediationStateAwaitingApproval)
	pending := stateTone(v1alpha1.RemediationStatePending)

	if waiting == pending {
		t.Errorf("AwaitingApproval and Pending share the tone %q; one needs a "+
			"person and the other needs nothing", waiting)
	}
}

// "In flight" covers two situations a reader acts on differently: waiting on
// remedik, and waiting on a person. Labelling an approval queue "pending or
// running" describes a busy operator when the truth is a queue nobody emptied.
func TestOverview_InFlightSaysWhichHalfIsWaitingOnAPerson(t *testing.T) {
	for _, tc := range []struct {
		name       string
		counts     stateCounts
		wantDetail string
		wantURL    string
	}{
		{
			name:       "nothing is waiting for a person",
			counts:     stateCounts{inFlight: 3},
			wantDetail: "pending or running",
			wantURL:    Filter{State: string(v1alpha1.RemediationStatePending)}.Path(),
		},
		{
			name:       "everything is waiting for a person",
			counts:     stateCounts{inFlight: 2, awaiting: 2},
			wantDetail: "waiting for a person",
			wantURL:    approvalsPath,
		},
		{
			name:       "some of each",
			counts:     stateCounts{inFlight: 5, awaiting: 2},
			wantDetail: "2 waiting for a person, 3 pending or running",
			wantURL:    approvalsPath,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inFlightDetail(tc.counts); got != tc.wantDetail {
				t.Errorf("detail = %q, want %q", got, tc.wantDetail)
			}
			// The link goes to whichever half somebody can act on.
			if got := inFlightURL(tc.counts); got != tc.wantURL {
				t.Errorf("URL = %q, want %q", got, tc.wantURL)
			}
		})
	}
}

// And the tally itself: a waiting record is in flight and also counted apart.
func TestOverview_AwaitingApprovalIsInFlightAndCountedApart(t *testing.T) {
	counts := tally([]*v1alpha1.Remediation{
		{Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateAwaitingApproval}},
		{Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateRunning}},
		{Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateSucceeded}},
	})
	if counts.awaiting != 1 {
		t.Errorf("awaiting = %d, want 1", counts.awaiting)
	}
	if counts.inFlight != 2 {
		t.Errorf("inFlight = %d, want 2: waiting for approval has not finished", counts.inFlight)
	}
}

// The activity panel is about when remediations ran, not when their records
// were written. The two differ for anything that did not arrive in real time —
// a restored backup, or a seeded demonstration cluster — and bucketing by
// creation piles a day of work into one bar and flattens the rest to nothing.
func TestActivity_BucketsByWhenItRanNotWhenItWasWritten(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 30, 0, 0, time.UTC)
	written := metav1.NewTime(now.Add(-time.Minute))

	ran := func(hoursAgo int) *v1alpha1.Remediation {
		at := metav1.NewTime(now.Add(-time.Duration(hoursAgo) * time.Hour))
		return &v1alpha1.Remediation{
			ObjectMeta: metav1.ObjectMeta{CreationTimestamp: written},
			Status: v1alpha1.RemediationStatus{
				State:     v1alpha1.RemediationStateSucceeded,
				StartedAt: &at,
			},
		}
	}

	panel := buildActivity([]*v1alpha1.Remediation{ran(1), ran(2), ran(3)}, now)

	if panel.Busiest != 1 {
		t.Errorf("busiest hour = %d, want 1: three runs an hour apart were "+
			"counted in one bar", panel.Busiest)
	}

	occupied := 0
	for _, bar := range panel.Bars {
		if bar.Total > 0 {
			occupied++
		}
	}
	if occupied != 3 {
		t.Errorf("occupied bars = %d, want 3", occupied)
	}
}

// A record that never ran has no start, so it counts where it exists.
func TestActivity_ARecordThatHasNotRunCountsWhereItExists(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 30, 0, 0, time.UTC)
	rem := &v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(now.Add(-90 * time.Minute)),
		},
		Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateAwaitingApproval},
	}

	if got := ranAt(rem); !got.Equal(rem.CreationTimestamp.Time) {
		t.Errorf("ranAt() = %v, want the creation time", got)
	}
	if panel := buildActivity([]*v1alpha1.Remediation{rem}, now); panel.Total != 0 {
		// It is in a bar, but it contributes to none of the three outcomes, so
		// the total counts nothing it did not do.
		t.Logf("total = %d", panel.Total)
	}
}

// A quiet day must not draw a wall. The chart scales against the busiest
// hour, and with a peak of one that made every occupied hour a full-height
// bar — twenty-four of them, which reads as a crisis and is a quiet night.
func TestActivity_AQuietDayDoesNotLookLikeAWall(t *testing.T) {
	now := testNow()

	// One execution in each of three different hours: peak is 1.
	records := []v1alpha1.Remediation{
		failedRemediation("a", 5),
		failedRemediation("b", 65),
		failedRemediation("c", 125),
	}
	panel := buildActivity(ptrs(records), now)

	if panel.Busiest != 1 {
		t.Fatalf("Busiest = %d, want 1 — the fixture puts one in each hour", panel.Busiest)
	}
	// The axis is labelled with what the top of the chart is worth, not with
	// the peak: saying "1" above bars that draw a quarter of the height is
	// the chart lying about its own scale.
	if panel.Scale != activityScaleFloor {
		t.Errorf("Scale = %d, want %d — the axis label must match the heights",
			panel.Scale, activityScaleFloor)
	}
	for _, bar := range panel.Bars {
		if bar.Total > 0 && bar.Percent > 100/activityScaleFloor {
			t.Errorf("an hour with %d execution(s) drew at %d%%, want no more than %d%%: "+
				"the peak is 1 and the chart still claimed a full bar",
				bar.Total, bar.Percent, 100/activityScaleFloor)
		}
	}
}

// Above the floor the scale is the real peak, so a busy hour still fills the
// chart and the comparison between hours is not compressed.
func TestActivity_ABusyDayStillScalesToItsOwnPeak(t *testing.T) {
	now := testNow()

	records := make([]v1alpha1.Remediation, 0, 6)
	for range 6 {
		records = append(records, failedRemediation("busy", 5))
	}

	panel := buildActivity(ptrs(records), now)
	if panel.Busiest != 6 {
		t.Fatalf("Busiest = %d, want 6", panel.Busiest)
	}

	var tallest int
	for _, bar := range panel.Bars {
		if bar.Percent > tallest {
			tallest = bar.Percent
		}
	}
	if tallest != 100 {
		t.Errorf("the busiest hour drew at %d%%, want 100 once the peak is above the floor", tallest)
	}
	if panel.Scale != panel.Busiest {
		t.Errorf("Scale = %d, want the real peak of %d above the floor", panel.Scale, panel.Busiest)
	}
}

// The bar encoded volume alone, and the rows at the top of this panel are
// nearly always within a few percent of each other — so it drew five
// identical tracks. The tail is the part that differs.
func TestBreakdown_TheBarCarriesTheFailures(t *testing.T) {
	records := []v1alpha1.Remediation{
		succeededRemediation("a", 30),
		succeededRemediation("b", 25),
		failedRemediation("c", 20),
		failedRemediation("d", 15),
	}
	for i := range records {
		records[i].Spec.Target = "deployment/payments/api"
	}

	rows := buildBreakdown(ptrs(records), targetNamespaceOf, byNamespace)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].FailedShare != 50 {
		t.Errorf("FailedShare = %d, want 50 — two of four failed", rows[0].FailedShare)
	}
}

// A row nothing failed in has no tail, rather than a zero-width one.
func TestBreakdown_NoFailuresDrawsNoTail(t *testing.T) {
	records := []v1alpha1.Remediation{succeededRemediation("a", 30)}
	records[0].Spec.Target = "deployment/payments/api"

	rows := buildBreakdown(ptrs(records), targetNamespaceOf, byNamespace)
	if rows[0].FailedShare != 0 {
		t.Errorf("FailedShare = %d, want 0", rows[0].FailedShare)
	}
}

// The panel said the same list twice — once as a sentence and once as chips
// under it — which made it twice as tall as the fact it carries. A few
// namespaces belong in the sentence; many belong in the chips; neither case
// wants both.
func TestPosturePanel_NamesThemOrChipsThemNeverBoth(t *testing.T) {
	few := buildPosturePanel(Posture{DryRun: true, Live: []string{"staging", "canary"}})
	if !strings.Contains(few.Detail, "staging and canary") {
		t.Errorf("Detail = %q, want it to name two namespaces", few.Detail)
	}
	if few.ShowNames {
		t.Error("ShowNames = true, want false: the sentence already named them")
	}

	many := buildPosturePanel(Posture{DryRun: true, Live: []string{"a", "b", "c", "d", "e"}})
	if !strings.Contains(many.Detail, "5 namespaces") {
		t.Errorf("Detail = %q, want it to count them rather than list five names", many.Detail)
	}
	if !many.ShowNames {
		t.Error("ShowNames = false, want true: the sentence only counted them")
	}
	if len(many.Live) != 5 {
		t.Errorf("Live = %v, want all five for the chips to render", many.Live)
	}
}

// The attention panel counts three sets and must link to the ones it
// counted. Both failure cards used to point at state=Failed, so the page
// offered a number of 64 and opened a list of 212.
func TestAttention_EachCardLinksToWhatItCounted(t *testing.T) {
	none := failedRemediation("no-escalation", 10)

	tried := failedRemediation("not-told", 10)
	tried.Status.Escalation = &v1alpha1.EscalationStatus{Phase: v1alpha1.StepPhaseFailed}

	panel := buildAttention([]*v1alpha1.Remediation{&none, &tried})

	seen := map[string]string{}
	for _, item := range panel.Items {
		seen[item.Label] = item.URL
	}

	unheard := seen["escalation failed"]
	untold := seen["failure with no escalation"]
	if unheard == "" || untold == "" {
		t.Fatalf("the panel did not raise both cards: %+v", seen)
	}
	if unheard == untold {
		t.Fatal("both cards still link to the same list")
	}
	if !strings.Contains(unheard, "escalation="+EscalationFailed) {
		t.Errorf("the unheard card links to %q, want the escalation clause it counted", unheard)
	}
	if !strings.Contains(untold, "escalation="+EscalationNone) {
		t.Errorf("the untold card links to %q, want the escalation clause it counted", untold)
	}
	// And each link has to reproduce the count, or it is a different lie.
	for label, link := range map[string]string{"unheard": unheard, "untold": untold} {
		query, err := url.ParseQuery(strings.TrimPrefix(link, remediationsPath+"?"))
		if err != nil {
			t.Fatalf("%s link %q does not parse: %v", label, link, err)
		}
		filter := ParseFilter(query)
		var kept int
		for _, rem := range []*v1alpha1.Remediation{&none, &tried} {
			if filter.Matches(rem) {
				kept++
			}
		}
		if kept != 1 {
			t.Errorf("%s link keeps %d records, want the 1 its card counted", label, kept)
		}
	}
}

// The tiles count every record the cluster still holds, which is a different
// question from "how is it going right now" — and the one the page's own
// heading asks. The window carries its own rate for that reason.
func TestActivity_TheWindowCarriesItsOwnFailureRate(t *testing.T) {
	now := testNow()
	records := []v1alpha1.Remediation{
		failedRemediation("bad-1", 5),
		failedRemediation("bad-2", 20),
		succeededRemediation("ok-1", 40),
		succeededRemediation("ok-2", 70),
		// Outside the window: it must not move the rate.
		failedRemediation("old", 60*40),
	}

	panel := buildActivity(ptrs(records), now)

	if panel.Total != 4 || panel.Failed != 2 {
		t.Fatalf("window = %d executions, %d failed; want 4 and 2", panel.Total, panel.Failed)
	}
	if got := panel.FailureRate(); got != 50 {
		t.Errorf("FailureRate() = %d, want 50", got)
	}

	// An empty window has no rate rather than a rate of zero, which reads as
	// "nothing is failing" when the truth is "nothing has run".
	if got := buildActivity(nil, now).FailureRate(); got != 0 {
		t.Errorf("an empty window reports %d", got)
	}
}

// The two tiles described the same ratio in two vocabularies: one a
// percentage, the other an opinion. They cannot be compared that way.
func TestFailedDetail_IsAShareLikeTheTileBesideIt(t *testing.T) {
	if got := failedDetail(0, 0); got != "none" {
		t.Errorf("failedDetail(0, 0) = %q, want none", got)
	}
	if got := failedDetail(3, 1); got != "25% of executed runs" {
		t.Errorf("failedDetail(3, 1) = %q, want 25%% of executed runs", got)
	}
	// And it agrees with its neighbour rather than contradicting it.
	if successRate(3, 1) != "75% of executed runs" {
		t.Errorf("successRate(3, 1) = %q", successRate(3, 1))
	}
}
