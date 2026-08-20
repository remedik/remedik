package dashboard

import (
	"net/url"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

// approved marks a record as one a person agreed to, which is what makes it
// not "handled without a person".
func approved(rem v1alpha1.Remediation) v1alpha1.Remediation {
	rem.Spec.Approval = &v1alpha1.Approval{
		Decision: v1alpha1.ApprovalApprove, By: "dana",
	}
	return rem
}

func TestImpact_HandledWithoutAPersonExcludesApprovalsAndSimulations(t *testing.T) {
	records := []v1alpha1.Remediation{
		succeededRemediation("a", 10),
		succeededRemediation("b", 20),
		succeededRemediation("c", 30),
		approved(succeededRemediation("agreed", 40)),
		failedRemediation("d", 50),
		failedRemediation("e", 60),
		// Nothing happened, so nothing was handled.
		simulatedRemediation("sim-1", "deployment/payments/api", 70),
		simulatedRemediation("sim-2", "deployment/payments/api", 80),
	}

	panel := buildImpact(ptrs(records), testWindow(), testNow())
	figure := panel.Figures[0]

	// Six ran for real; three of them succeeded with nobody agreeing to them.
	if figure.Value != "50%" {
		t.Errorf("handled without a person = %s, want 50%%", figure.Value)
	}
	if !strings.Contains(figure.Detail, "3 of 6 executions") {
		t.Errorf("detail = %q, want it to show the arithmetic", figure.Detail)
	}
}

// Two records have a median, and it is noise dressed as a measurement.
func TestImpact_TheMedianIsWithheldBelowTheFloor(t *testing.T) {
	var few []v1alpha1.Remediation
	for i := range medianFloor - 1 {
		few = append(few, succeededRemediation("ok-"+string(rune('a'+i)), i+1))
	}

	panel := buildImpact(ptrs(few), testWindow(), testNow())
	figure := panel.Figures[1]

	if figure.Value != "—" {
		t.Errorf("median over %d records = %q, want it withheld", len(few), figure.Value)
	}
	if !strings.Contains(figure.Detail, "too few for a median") {
		t.Errorf("detail = %q, want it to say why nothing is shown", figure.Detail)
	}

	// One more record, and it is worth computing.
	enough := make([]v1alpha1.Remediation, len(few), len(few)+1)
	copy(enough, few)
	enough = append(enough, succeededRemediation("ok-last", 9))
	got := buildImpact(ptrs(enough), testWindow(), testNow()).Figures[1]
	if got.Value == "—" {
		t.Errorf("median over %d records is still withheld", len(enough))
	}
}

// Without a direction a percentage is a mood. The comparison is against the
// previous window of exactly equal length.
func TestImpact_DirectionComparesTheWindowBefore(t *testing.T) {
	day := int(24 * time.Hour / time.Minute)

	records := []v1alpha1.Remediation{
		// This window: two of two succeeded.
		succeededRemediation("now-1", 30),
		succeededRemediation("now-2", 60),
		// The window before: one of two.
		succeededRemediation("then-1", day+30),
		failedRemediation("then-2", day+60),
	}

	panel := buildImpact(ptrs(records), testWindow(), testNow())
	if !panel.Comparable {
		t.Fatal("an earlier window exists and the panel says it cannot compare")
	}

	figure := panel.Figures[0]
	if figure.Value != "100%" {
		t.Errorf("this window = %s, want 100%%", figure.Value)
	}
	// Points, not percent: a rise from 50 to 100 is fifty points, and calling
	// it "100% better" is the oldest way to mislead with a true number.
	if figure.Delta != "up 50 points" {
		t.Errorf("delta = %q, want it in points", figure.Delta)
	}
	if figure.Direction != directionBetter {
		t.Errorf("direction = %q, want better", figure.Direction)
	}
}

// With nothing before it, a figure has a value and no direction — rather than
// a direction computed against zero, which would read as a collapse.
func TestImpact_NoEarlierWindowMeansNoDirection(t *testing.T) {
	panel := buildImpact(ptrs([]v1alpha1.Remediation{
		succeededRemediation("only", 5),
	}), testWindow(), testNow())

	if panel.Comparable {
		t.Error("nothing ran in the window before, and the panel claims a comparison")
	}
	for _, figure := range panel.Figures {
		if figure.Delta != "" || figure.Direction != "" {
			t.Errorf("%s carries a direction with nothing to compare to: %q %q",
				figure.Label, figure.Delta, figure.Direction)
		}
	}
}

// Retention prunes records, so a seven-day window may be describing three
// days. The panel states what it actually covered rather than implying the
// label.
func TestImpact_StatesTheRangeItActuallyCovered(t *testing.T) {
	week := windows[1]
	records := []v1alpha1.Remediation{
		succeededRemediation("kept-1", 60*24*2),
		succeededRemediation("kept-2", 60*12),
	}

	panel := buildImpact(ptrs(records), week, testNow())
	if panel.Covered != "2 days" {
		t.Errorf("covered = %q, want the two days actually held", panel.Covered)
	}

	// And when the window is genuinely full, it says nothing extra.
	full := make([]v1alpha1.Remediation, len(records), len(records)+1)
	copy(full, records)
	full = append(full, succeededRemediation("kept-0", 60*24*7-30))
	if got := buildImpact(ptrs(full), week, testNow()).Covered; got != "" {
		t.Errorf("covered = %q on a full window, want no qualification", got)
	}
}

// The range is a link like every other choice on these pages, and an unknown
// one is the default rather than an error.
func TestWindow_IsALinkAndUnknownIsTheDefault(t *testing.T) {
	if got := ParseWindow(url.Values{paramRange: {"7d"}}).Key; got != "7d" {
		t.Errorf("range=7d parsed as %q", got)
	}
	if got := ParseWindow(url.Values{paramRange: {"nonsense"}}).Key; got != windows[0].Key {
		t.Errorf("an unknown range gave %q, want the default", got)
	}
	if got := ParseWindow(nil).Key; got != windows[0].Key {
		t.Errorf("no range gave %q, want the default", got)
	}

	// The default carries no parameter, so the front page stays a bare "/".
	if got := windows[0].URL(); got != "/" {
		t.Errorf("default window URL = %q, want /", got)
	}
	if got := windows[1].URL(); got != "/?range=7d" {
		t.Errorf("week window URL = %q", got)
	}
}

// A week is seven daily buckets, not a hundred and sixty-eight hourly ones.
func TestActivity_TheWeekBucketsByDay(t *testing.T) {
	week := windows[1]
	records := []v1alpha1.Remediation{
		succeededRemediation("today", 30),
		succeededRemediation("days-ago", 60*24*3),
		// Older than the window, so it is not in any bucket.
		succeededRemediation("ancient", 60*24*30),
	}

	panel := buildActivity(ptrs(records), week, testNow())

	if len(panel.Bars) != 7 {
		t.Fatalf("bars = %d, want 7", len(panel.Bars))
	}
	if panel.Total != 2 {
		t.Errorf("total = %d, want the two inside the window", panel.Total)
	}
	if panel.Window != "last 7 days" {
		t.Errorf("window = %q", panel.Window)
	}
}

// The whole page moves together: asking for a week gives a week of chart and a
// week of arithmetic, not one of each.
func TestOverview_TheWindowGovernsBothPanels(t *testing.T) {
	h, reader := newHandler(t, Config{})
	reader.remediations = []v1alpha1.Remediation{
		succeededRemediation("recent", 30),
		succeededRemediation("older", 60*24*4),
	}
	reader.strategies = []v1alpha1.RemediationStrategy{enabledStrategy()}

	body := get(t, h, "/?range=7d", nil).Body.String()
	mustContain(t, body, "last 7 days", "describe the week it was asked for")
	mustNotContain(t, body, "last 24 hours", "still describe a day somewhere")
}

// A record with a firing time in the future, or an outcome before its alert,
// is clock skew rather than a negative resolution.
func TestImpact_SkewedTimestampsAreNotNegativeDurations(t *testing.T) {
	rem := succeededRemediation("skewed", 5)
	future := metav1.NewTime(testNow().Add(time.Hour))
	rem.Spec.Alert.StartsAt = &future

	if _, ok := resolution(&rem); ok {
		t.Error("an outcome recorded before its alert fired counted as a resolution")
	}
}
