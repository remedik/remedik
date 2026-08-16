package dashboard

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ratyx/remedik/api/v1alpha1"
)

func TestBuildOverviewCountsEveryState(t *testing.T) {
	view := buildOverview(
		[]v1alpha1.Remediation{
			simulatedRemediation("sim-1", "deployment/payments/api", 40),
			simulatedRemediation("sim-2", "deployment/payments/api", 30),
			succeededRemediation("ok-1", 20),
			failedRemediation("bad-1", 10),
			pendingRemediation("new-1", 1),
		},
		[]v1alpha1.RemediationStrategy{enabledStrategy(), disabledStrategy()},
		true,
		testNow(),
	)

	if view.Total != 5 {
		t.Errorf("Total = %d, want 5", view.Total)
	}
	if view.EnabledCount != 1 {
		t.Errorf("EnabledCount = %d, want 1", view.EnabledCount)
	}

	want := map[string]string{
		"Executions": "5",
		"Succeeded":  "1",
		"Failed":     "1",
		"Simulated":  "2",
		"In flight":  "1",
	}
	for _, stat := range view.Stats {
		if expected, ok := want[stat.Label]; ok && stat.Value != expected {
			t.Errorf("%s = %s, want %s", stat.Label, stat.Value, expected)
		}
		delete(want, stat.Label)
	}
	if len(want) != 0 {
		t.Errorf("the overview is missing these figures: %v", want)
	}
}

// A record whose status has not been written yet counts as in flight: to an
// operator, "created and not picked up" is not a finished thing.
func TestAnUnreconciledRecordCountsAsInFlight(t *testing.T) {
	rem := pendingRemediation("new-1", 1)
	rem.Status.State = ""

	view := buildOverview([]v1alpha1.Remediation{rem}, nil, false, testNow())

	for _, stat := range view.Stats {
		if stat.Label == "In flight" && stat.Value != "1" {
			t.Errorf("In flight = %s, want 1", stat.Value)
		}
	}
	if got := view.Recent[0].State; got != "Pending" {
		t.Errorf("an empty state renders as %q, want Pending", got)
	}
}

func TestOverviewOrdersNewestFirstAndBreaksTiesByName(t *testing.T) {
	same := 30
	view := buildOverview(
		[]v1alpha1.Remediation{
			succeededRemediation("b-older", same),
			succeededRemediation("a-older", same),
			succeededRemediation("c-newest", 1),
		},
		nil, false, testNow(),
	)

	got := []string{view.Recent[0].Name, view.Recent[1].Name, view.Recent[2].Name}
	want := []string{"c-newest", "a-older", "b-older"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (ties must break by name, or rows move "+
				"between refreshes of the same data)", got, want)
		}
	}
}

func TestDryRunReport(t *testing.T) {
	report := buildDryRunReport(
		sorted([]v1alpha1.Remediation{
			simulatedRemediation("sim-1", "deployment/payments/api", 60*24*2),
			simulatedRemediation("sim-2", "deployment/payments/api", 120),
			simulatedRemediation("sim-3", "deployment/payments/worker", 30),
			succeededRemediation("ok-1", 10),
		}),
		true, testNow(),
	)

	if report == nil {
		t.Fatal("buildDryRunReport() = nil, want a report")
	}
	if report.Simulated != 3 {
		t.Errorf("Simulated = %d, want 3 (a real run must not be counted)", report.Simulated)
	}
	if report.Targets != 2 {
		t.Errorf("Targets = %d, want 2 distinct targets", report.Targets)
	}
	if report.Period != "2 days" {
		t.Errorf("Period = %q, want \"2 days\"", report.Period)
	}
	if len(report.ByStrategy) != 1 {
		t.Fatalf("ByStrategy has %d entries, want 1", len(report.ByStrategy))
	}

	tally := report.ByStrategy[0]
	if tally.Strategy != "pod-crashloop" || tally.Count != 3 || tally.Share != 100 {
		t.Errorf("tally = %+v, want pod-crashloop with 3 of 3", tally)
	}
	// The example is the most recent simulation, because "what would this
	// have done" is a question about now, not about two days ago.
	if want := "patch deployment deployment/payments/worker restartedAt annotation"; tally.Example != want {
		t.Errorf("Example = %q, want %q", tally.Example, want)
	}
}

func TestDryRunReportExistsForATrialThatHasProducedNothing(t *testing.T) {
	if report := buildDryRunReport(nil, true, testNow()); report == nil {
		t.Error("dry-run is on and produced no report; a trial with no results is " +
			"itself worth stating")
	}
	if report := buildDryRunReport(nil, false, testNow()); report != nil {
		t.Error("dry-run is off and nothing was simulated, but a report was built")
	}
}

// Turning dry-run off must not erase the report the decision was based on.
func TestDryRunReportSurvivesDryRunBeingTurnedOff(t *testing.T) {
	report := buildDryRunReport(
		[]v1alpha1.Remediation{simulatedRemediation("sim-1", "deployment/payments/api", 60)},
		false, testNow(),
	)

	if report == nil {
		t.Fatal("no report for existing simulations with dry-run off")
	}
	if report.Active {
		t.Error("Active = true, want false when dry-run is off")
	}
}

func TestBuildStepsJoinsThePlanWithWhatHappened(t *testing.T) {
	rem := failedRemediation("bad-1", 10)
	steps := buildSteps(&rem)

	if len(steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(steps))
	}
	if steps[0].Phase != string(v1alpha1.StepPhaseSucceeded) {
		t.Errorf("step 1 phase = %q, want Succeeded", steps[0].Phase)
	}
	if steps[1].Phase != string(v1alpha1.StepPhaseFailed) || steps[1].Message == "" {
		t.Errorf("step 2 = %+v, want Failed with a message", steps[1])
	}
	if steps[2].Phase != string(v1alpha1.StepPhaseSkipped) {
		t.Errorf("step 3 phase = %q, want Skipped", steps[2].Phase)
	}
	// The parameters come from the plan on the spec, which the status does
	// not repeat.
	if len(steps[1].Params) != 1 || steps[1].Params[0].Value != "worker" {
		t.Errorf("step 2 params = %+v, want the declared deployment=worker", steps[1].Params)
	}
}

// A run interrupted partway has more planned steps than recorded ones. The
// steps that never started must still be listed: their absence is the thing
// someone reading the failure needs to see.
func TestBuildStepsShowsPlannedStepsThatNeverStarted(t *testing.T) {
	rem := failedRemediation("bad-1", 10)
	rem.Status.Steps = rem.Status.Steps[:1]
	rem.Status.Reason = v1alpha1.ReasonInterrupted

	steps := buildSteps(&rem)

	if len(steps) != 3 {
		t.Fatalf("got %d steps, want all 3 planned ones", len(steps))
	}
	if steps[1].Ran || steps[2].Ran {
		t.Error("steps with no recorded outcome are marked as having run")
	}
	if steps[2].Phase != string(v1alpha1.StepPhasePending) {
		t.Errorf("an unstarted step reads as %q, want Pending", steps[2].Phase)
	}
}

func TestSummaryExplainsEachOutcome(t *testing.T) {
	tests := []struct {
		name string
		rem  func() v1alpha1.Remediation
		want string
	}{
		{
			name: "simulated",
			rem:  func() v1alpha1.Remediation { return simulatedRemediation("s", "d/n/a", 5) },
			want: "nothing in the cluster was changed",
		},
		{
			name: "succeeded",
			rem:  func() v1alpha1.Remediation { return succeededRemediation("s", 5) },
			want: "Completed 1 step",
		},
		{
			name: "failed step",
			rem:  func() v1alpha1.Remediation { return failedRemediation("f", 5) },
			want: "Step 2 (deployment.restart) failed",
		},
		{
			name: "interrupted",
			rem: func() v1alpha1.Remediation {
				rem := failedRemediation("i", 5)
				rem.Status.Reason = v1alpha1.ReasonInterrupted
				return rem
			},
			want: "failed rather than resumed",
		},
		{
			name: "unknown action",
			rem: func() v1alpha1.Remediation {
				rem := failedRemediation("u", 5)
				rem.Status.Reason = v1alpha1.ReasonUnknownAction
				rem.Status.Message = `unknown action "node.drain"`
				return rem
			},
			want: `unknown action "node.drain"`,
		},
		{
			name: "waiting to be picked up",
			rem:  func() v1alpha1.Remediation { return pendingRemediation("p", 1) },
			want: "waiting for the reconciler",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rem := tc.rem()
			got := summarise(&rem, buildSteps(&rem))
			if !strings.Contains(got, tc.want) {
				t.Errorf("summary = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestBuildStrategies(t *testing.T) {
	view := buildStrategies(
		// Deliberately out of order: the page must not depend on what the
		// API server happened to return first.
		[]v1alpha1.RemediationStrategy{disabledStrategy(), enabledStrategy()},
		[]v1alpha1.Remediation{
			simulatedRemediation("sim-1", "deployment/payments/api", 30),
			failedRemediation("bad-1", 10),
		},
		testNow(),
	)

	if view.Total != 2 || view.Enabled != 1 || view.Disabled != 1 {
		t.Errorf("counts = total %d, enabled %d, disabled %d; want 2, 1, 1",
			view.Total, view.Enabled, view.Disabled)
	}
	if view.Strategies[0].Name != "node-drain" {
		t.Errorf("strategies are not sorted by name: first is %q", view.Strategies[0].Name)
	}

	crashloop := view.Strategies[1]
	if crashloop.Cooldown != "15m" {
		t.Errorf("Cooldown = %q, want \"15m\" — the form it was written in", crashloop.Cooldown)
	}
	if crashloop.MaxPerHour != "4" {
		t.Errorf("MaxPerHour = %q, want \"4\"", crashloop.MaxPerHour)
	}
	if !crashloop.HasGuards() {
		t.Error("HasGuards() = false for a strategy with both guards set")
	}
	if crashloop.Simulated != 1 || crashloop.Failed != 1 {
		t.Errorf("tallies = %d simulated, %d failed; want 1 and 1",
			crashloop.Simulated, crashloop.Failed)
	}
	if len(crashloop.Recent) != 2 {
		t.Errorf("Recent has %d rows, want 2", len(crashloop.Recent))
	}

	drain := view.Strategies[0]
	if drain.Enabled {
		t.Error("a strategy with enabled: false reads as enabled")
	}
	if drain.HasGuards() {
		t.Error("HasGuards() = true for a strategy that sets neither guard")
	}
	// An unset mode is "auto": the field defaults in the schema, and a blank
	// cell would read as "unknown" rather than "the only mode there is".
	if drain.Mode != string(v1alpha1.ExecutionModeAuto) {
		t.Errorf("Mode = %q, want auto for an unset mode", drain.Mode)
	}
}

func TestStrategyRunCountFallsBackToTheRecords(t *testing.T) {
	strategy := enabledStrategy()
	strategy.Status.ExecutionCount = 0

	view := buildStrategies(
		[]v1alpha1.RemediationStrategy{strategy},
		[]v1alpha1.Remediation{
			simulatedRemediation("sim-1", "deployment/payments/api", 30),
			failedRemediation("bad-1", 10),
		},
		testNow(),
	)

	if got := view.Strategies[0].Runs; got != 2 {
		t.Errorf("Runs = %d, want 2 counted from the records when the status lags", got)
	}
}

func TestSortedLabelsIsStable(t *testing.T) {
	labels := sortedLabels(map[string]string{"z": "1", "a": "2", "m": "3"})

	want := []string{"a", "m", "z"}
	for i, label := range labels {
		if label.Key != want[i] {
			t.Fatalf("labels = %+v, want them sorted by key", labels)
		}
	}
	if sortedLabels(nil) != nil {
		t.Error("sortedLabels(nil) is not nil, so an empty list would render as a heading")
	}
}

// --------------------------------------------------------------------------
// Formatting
// --------------------------------------------------------------------------

func TestFormatAge(t *testing.T) {
	now := testNow()
	tests := []struct {
		d    time.Duration
		want string
	}{
		{d: -time.Minute, want: "0s"}, // clock skew must not print a negative age
		{d: 5 * time.Second, want: "5s"},
		{d: 90 * time.Second, want: "1m"},
		{d: 3 * time.Hour, want: "3h"},
		{d: 50 * time.Hour, want: "2d"},
		{d: 800 * 24 * time.Hour, want: "2y"},
	}

	for _, tc := range tests {
		if got := FormatAge(now.Add(-tc.d), now); got != tc.want {
			t.Errorf("FormatAge(-%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{d: 820 * time.Millisecond, want: "820ms"},
		{d: 1500 * time.Millisecond, want: "1.5s"},
		{d: 130 * time.Second, want: "2m 10s"},
		{d: 90 * time.Minute, want: "1h 30m"},
	}

	for _, tc := range tests {
		if got := FormatDuration(tc.d); got != tc.want {
			t.Errorf("FormatDuration(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFormatSpanNeedsBothEnds(t *testing.T) {
	start := metav1.NewTime(testNow())
	if got := FormatSpan(&start, nil); got != "" {
		t.Errorf("FormatSpan with no end = %q, want empty: a running step has no duration", got)
	}
	if got := FormatSpan(nil, nil); got != "" {
		t.Errorf("FormatSpan(nil, nil) = %q, want empty", got)
	}
}

func TestShortDurationRendersGuardsAsWritten(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{d: 15 * time.Minute, want: "15m"},
		{d: 2 * time.Hour, want: "2h"},
		{d: 30 * time.Second, want: "30s"},
		// Not "1m30s": a guard written as `cooldown: 90s` should read back
		// the way it was written.
		{d: 90 * time.Second, want: "90s"},
		{d: 1500 * time.Millisecond, want: "1.5s"},
	}

	for _, tc := range tests {
		if got := shortDuration(tc.d); got != tc.want {
			t.Errorf("shortDuration(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestPluralHandlesIrregularUnits(t *testing.T) {
	tests := []struct {
		n    int
		unit string
		want string
	}{
		{n: 1, unit: "step", want: "1 step"},
		{n: 2, unit: "step", want: "2 steps"},
		{n: 1, unit: "strategy-strategies", want: "1 strategy"},
		{n: 0, unit: "strategy-strategies", want: "0 strategies"},
		{n: 1, unit: "retry-retries", want: "1 retry"},
	}

	for _, tc := range tests {
		if got := plural(tc.n, tc.unit); got != tc.want {
			t.Errorf("plural(%d, %q) = %q, want %q", tc.n, tc.unit, got, tc.want)
		}
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

func sorted(remediations []v1alpha1.Remediation) []v1alpha1.Remediation {
	sortNewestFirst(remediations)
	return remediations
}
