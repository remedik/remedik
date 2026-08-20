package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

// in returns rem retargeted at a namespace, so a test can say which
// namespace it means without restating a whole fixture.
func in(rem v1alpha1.Remediation, namespace, workload string) v1alpha1.Remediation {
	rem.Spec.Target = "deployment/" + namespace + "/" + workload
	return rem
}

func TestBuildNamespaces_OneRowPerNamespace(t *testing.T) {
	view := buildNamespaces([]v1alpha1.Remediation{
		in(succeededRemediation("a", 10), "payments", "api"),
		in(succeededRemediation("b", 9), "payments", "worker"),
		in(succeededRemediation("c", 8), "checkout", "web"),
	}, Posture{}, testNow(), NamespaceFilter{})

	if view.Total != 2 {
		t.Fatalf("Total = %d, want 2 namespaces", view.Total)
	}
	if view.Executions != 3 {
		t.Errorf("Executions = %d, want 3", view.Executions)
	}
	// Nothing is wrong anywhere, so both are quiet and the page says so.
	if !view.AllQuiet() {
		t.Errorf("AllQuiet() = false with no failures anywhere")
	}
	if len(view.Rest) != 2 {
		t.Fatalf("rest rows = %d, want 2", len(view.Rest))
	}
	if view.Rest[0].Name != "payments" || view.Rest[0].Total != 2 {
		t.Errorf("first row of the rest = %s with %d, want payments with 2",
			view.Rest[0].Name, view.Rest[0].Total)
	}
}

// The ordering is the page's whole argument: a list somebody has to read all
// of is a list that does not answer "where is this going badly".
func TestBuildNamespaces_UnheardFailuresComeFirst(t *testing.T) {
	escalated := in(failedRemediation("told", 5), "quiet", "api")
	escalated.Status.Escalation = &v1alpha1.EscalationStatus{
		Phase: v1alpha1.StepPhaseSucceeded,
	}

	// A namespace with far more traffic, but nothing wrong.
	var busy []v1alpha1.Remediation
	for i := range 20 {
		busy = append(busy, in(succeededRemediation(string(rune('a'+i)), i), "busy", "api"))
	}

	remediations := make([]v1alpha1.Remediation, 0, len(busy)+2)
	remediations = append(remediations, busy...)
	remediations = append(remediations,
		escalated,
		in(failedRemediation("unheard", 4), "silent", "api"),
	)

	view := buildNamespaces(remediations, Posture{}, testNow(), NamespaceFilter{})

	if len(view.Rows) != 2 {
		t.Fatalf("rows needing attention = %d, want 2", len(view.Rows))
	}
	got := []string{view.Rows[0].Name, view.Rows[1].Name}
	want := []string{"silent", "quiet"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v — a failure nobody heard about "+
				"must outrank one somebody has seen", got, want)
		}
	}

	// The busy namespace has nothing wrong, so however much traffic it
	// carries it belongs in the quiet table rather than above a failure.
	if len(view.Rest) != 1 || view.Rest[0].Name != "busy" {
		t.Errorf("rest = %+v, want just busy", view.Rest)
	}
}

func TestBuildNamespaces_CountsFailuresNobodyWasTold(t *testing.T) {
	told := in(failedRemediation("told", 5), "payments", "api")
	told.Status.Escalation = &v1alpha1.EscalationStatus{Phase: v1alpha1.StepPhaseSucceeded}

	// An escalation that itself failed is a failure nobody was told about:
	// the distinction is whether the message left, not whether one was
	// declared.
	tried := in(failedRemediation("tried", 4), "payments", "api")
	tried.Status.Escalation = &v1alpha1.EscalationStatus{Phase: v1alpha1.StepPhaseFailed}

	silent := in(failedRemediation("silent", 3), "payments", "api")

	view := buildNamespaces([]v1alpha1.Remediation{told, tried, silent},
		Posture{}, testNow(), NamespaceFilter{})

	row := view.Rows[0]
	if row.Failed != 3 {
		t.Fatalf("Failed = %d, want 3", row.Failed)
	}
	if row.Unheard != 2 {
		t.Errorf("Unheard = %d, want 2 — a failed escalation told nobody", row.Unheard)
	}
	if row.Tone != toneFailed {
		t.Errorf("Tone = %q, want %q", row.Tone, toneFailed)
	}
	if !strings.Contains(row.Note, "nobody was told") {
		t.Errorf("Note = %q, want it to say nobody was told", row.Note)
	}
}

// A failure somebody has already seen must not look like one nobody heard
// about — the page exists to tell those apart.
func TestBuildNamespaces_AnEscalatedFailureIsAWarningNotAnAlarm(t *testing.T) {
	told := in(failedRemediation("told", 5), "payments", "api")
	told.Status.Escalation = &v1alpha1.EscalationStatus{Phase: v1alpha1.StepPhaseSucceeded}

	view := buildNamespaces([]v1alpha1.Remediation{told}, Posture{}, testNow(), NamespaceFilter{})

	if view.Rows[0].Tone != toneWarn {
		t.Errorf("Tone = %q, want %q", view.Rows[0].Tone, toneWarn)
	}
	if view.Rows[0].Unheard != 0 {
		t.Errorf("Unheard = %d, want 0", view.Rows[0].Unheard)
	}
}

func TestBuildNamespaces_PostureIsPerRow(t *testing.T) {
	posture := Posture{DryRun: true, Live: []string{"staging"}}

	view := buildNamespaces([]v1alpha1.Remediation{
		in(succeededRemediation("a", 5), "staging", "api"),
		in(simulatedRemediation("b", "", 4), "prod", "api"),
	}, posture, testNow(), NamespaceFilter{})

	byName := map[string]NamespaceRow{}
	for _, row := range append(view.Rows, view.Rest...) {
		byName[row.Name] = row
	}

	if got := byName["staging"].Posture; got != "Live" {
		t.Errorf("staging posture = %q, want Live", got)
	}
	if got := byName["prod"].Posture; got != "Reporting" {
		t.Errorf("prod posture = %q, want Reporting — it is not in Live", got)
	}
}

// The inverse: a live default with a namespace held back.
func TestBuildNamespaces_PostureHonoursDryRunOnly(t *testing.T) {
	posture := Posture{DryRun: false, DryRunOnly: []string{"prod"}}

	view := buildNamespaces([]v1alpha1.Remediation{
		in(succeededRemediation("a", 5), "staging", "api"),
		in(simulatedRemediation("b", "", 4), "prod", "api"),
	}, posture, testNow(), NamespaceFilter{})

	for _, row := range append(view.Rows, view.Rest...) {
		want := "Live"
		if row.Name == "prod" {
			want = "Reporting"
		}
		if row.Posture != want {
			t.Errorf("%s posture = %q, want %q", row.Name, row.Posture, want)
		}
	}
}

// A namespace that has only ever been simulated has no success rate: it never
// ran. Reporting 0% would read as failure.
func TestBuildNamespaces_SimulationIsNotAFailedRate(t *testing.T) {
	view := buildNamespaces([]v1alpha1.Remediation{
		in(simulatedRemediation("a", "", 5), "prod", "api"),
		in(simulatedRemediation("b", "", 4), "prod", "api"),
	}, Posture{DryRun: true}, testNow(), NamespaceFilter{})

	row := view.Rest[0]
	if row.RatePct != -1 {
		t.Errorf("RatePct = %d, want -1 for a namespace where nothing ran", row.RatePct)
	}
	if !strings.Contains(row.Rate, "nothing ran") {
		t.Errorf("Rate = %q, want it to say nothing ran for real", row.Rate)
	}
	if row.Tone != toneDryRun {
		t.Errorf("Tone = %q, want %q", row.Tone, toneDryRun)
	}
	if row.NeedsAttention() {
		t.Error("a namespace that only ever reported was marked as needing attention")
	}
}

// A node action names no namespace. It belongs on the list, not on a page
// where every row is a namespace.
func TestBuildNamespaces_ClusterScopedRecordsAreNotARow(t *testing.T) {
	node := succeededRemediation("drain", 5)
	node.Spec.Target = "node/worker-3"

	view := buildNamespaces([]v1alpha1.Remediation{
		node,
		in(succeededRemediation("a", 4), "payments", "api"),
	}, Posture{}, testNow(), NamespaceFilter{})

	if view.Total != 1 {
		t.Fatalf("Total = %d, want 1 — a node action is not a namespace", view.Total)
	}
	if view.Rest[0].Name != "payments" {
		t.Errorf("row = %q, want payments", view.Rest[0].Name)
	}
}

func TestBuildNamespaces_EmptyIsNotAnError(t *testing.T) {
	view := buildNamespaces(nil, Posture{}, testNow(), NamespaceFilter{})

	if view.Any() {
		t.Error("Any() = true with no records")
	}
	if view.Total != 0 || view.Attention != 0 {
		t.Errorf("view = %+v, want zeroes", view)
	}
}

// The order must not shuffle between refreshes: a page that reorders itself
// while somebody reads it is worse than one that is sorted badly.
func TestBuildNamespaces_OrderIsStable(t *testing.T) {
	remediations := []v1alpha1.Remediation{
		in(succeededRemediation("a", 5), "zeta", "api"),
		in(succeededRemediation("b", 4), "alpha", "api"),
		in(succeededRemediation("c", 3), "mid", "api"),
	}

	first := buildNamespaces(remediations, Posture{}, testNow(), NamespaceFilter{})
	for range 20 {
		again := buildNamespaces(remediations, Posture{}, testNow(), NamespaceFilter{})
		for i := range first.Rest {
			if first.Rest[i].Name != again.Rest[i].Name {
				t.Fatalf("order changed between builds: %s then %s",
					first.Rest[i].Name, again.Rest[i].Name)
			}
		}
	}

	// Equal on every count, so the name decides.
	want := []string{"alpha", "mid", "zeta"}
	for i, name := range want {
		if first.Rest[i].Name != name {
			t.Errorf("row %d = %s, want %s", i, first.Rest[i].Name, name)
		}
	}
}

func TestBuildNamespaces_LastActivityIsTheNewestRecord(t *testing.T) {
	old := in(succeededRemediation("old", 600), "payments", "api")
	recent := in(succeededRemediation("recent", 3), "payments", "api")

	view := buildNamespaces([]v1alpha1.Remediation{old, recent}, Posture{}, testNow(), NamespaceFilter{})

	if got := view.Rest[0].Last; got != "3m" {
		t.Errorf("Last = %q, want 3m — the newest record, not the first seen", got)
	}
}

// In-flight work is counted, not silently dropped: a row whose numbers do not
// add up to its total is a row somebody will not trust.
func TestBuildNamespaces_CountsAddUp(t *testing.T) {
	pending := pendingRemediation("waiting", 1)
	pending.Spec.Target = "deployment/payments/api"

	view := buildNamespaces([]v1alpha1.Remediation{
		in(succeededRemediation("a", 5), "payments", "api"),
		in(failedRemediation("b", 4), "payments", "api"),
		in(simulatedRemediation("c", "", 3), "payments", "api"),
		pending,
	}, Posture{}, testNow(), NamespaceFilter{})

	row := view.Rows[0]
	sum := row.Succeeded + row.Failed + row.Simulated + row.InFlight
	if sum != row.Total {
		t.Errorf("%d+%d+%d+%d = %d, want Total %d",
			row.Succeeded, row.Failed, row.Simulated, row.InFlight, sum, row.Total)
	}
	if row.InFlight != 1 {
		t.Errorf("InFlight = %d, want 1", row.InFlight)
	}
}

// A record with no creation timestamp at all must not crash the page.
func TestBuildNamespaces_SurvivesAnEmptyRecord(t *testing.T) {
	bare := v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{Name: "bare", Namespace: testNamespace},
		Spec:       v1alpha1.RemediationSpec{Target: "deployment/payments/api"},
	}

	view := buildNamespaces([]v1alpha1.Remediation{bare}, Posture{}, testNow(), NamespaceFilter{})

	if view.Total != 1 {
		t.Fatalf("Total = %d, want 1", view.Total)
	}
	if view.Rest[0].InFlight != 1 {
		t.Errorf("InFlight = %d, want 1 — an empty state has not been picked up yet",
			view.Rest[0].InFlight)
	}
}

// "1 need attention" is the kind of thing a reader notices immediately.
func TestNamespacesView_AttentionLabelConjugates(t *testing.T) {
	for n, want := range map[int]string{
		0: "0 need attention",
		1: "1 needs attention",
		2: "2 need attention",
	} {
		view := NamespacesView{Attention: n}
		if got := view.AttentionLabel(); got != want {
			t.Errorf("AttentionLabel() with %d = %q, want %q", n, got, want)
		}
	}
}

// The page's answer to scale: the namespaces with something wrong are shown
// in full however many there are, and the rest become one compact table.
//
// Paging by severity would be worse than either. Page two of a list ordered
// by "what needs attention" is by construction the part that does not.
func TestBuildNamespaces_QuietNamespacesDoNotCrowdOutTheLoudOnes(t *testing.T) {
	var remediations []v1alpha1.Remediation

	// A hundred and fifty namespaces where nothing is wrong.
	for i := range 150 {
		ns := fmt.Sprintf("quiet-%03d", i)
		remediations = append(remediations,
			in(succeededRemediation(fmt.Sprintf("ok-%03d", i), i%60), ns, "api"))
	}
	// And one where a failure was never escalated.
	remediations = append(remediations,
		in(failedRemediation("unheard", 1), "the-one-that-matters", "api"))

	view := buildNamespaces(remediations, Posture{}, testNow(), NamespaceFilter{})

	if view.Total != 151 {
		t.Fatalf("Total = %d, want 151", view.Total)
	}
	if len(view.Rows) != 1 {
		t.Fatalf("rows needing attention = %d, want 1 — the quiet ones must not "+
			"be mixed in with the one somebody has to look at", len(view.Rows))
	}
	if view.Rows[0].Name != "the-one-that-matters" {
		t.Errorf("first row = %q, want the-one-that-matters", view.Rows[0].Name)
	}
	if len(view.Rest) != 150 {
		t.Errorf("rest rows = %d, want 150", len(view.Rest))
	}
	if view.AllQuiet() {
		t.Error("AllQuiet() = true although one namespace needs attention")
	}
}

// The good case has to look different from the empty one: "remedik has run
// here and nothing is wrong" and "remedik has never run" are opposite
// answers, and a page that renders them the same is a page nobody trusts.
func TestBuildNamespaces_AllClearIsNotTheSameAsEmpty(t *testing.T) {
	allClear := buildNamespaces([]v1alpha1.Remediation{
		in(succeededRemediation("a", 5), "payments", "api"),
	}, Posture{}, testNow(), NamespaceFilter{})

	if !allClear.Any() || !allClear.AllQuiet() {
		t.Errorf("Any()=%v AllQuiet()=%v, want true and true", allClear.Any(), allClear.AllQuiet())
	}

	empty := buildNamespaces(nil, Posture{}, testNow(), NamespaceFilter{})
	if empty.Any() || empty.AllQuiet() {
		t.Errorf("Any()=%v AllQuiet()=%v, want false and false", empty.Any(), empty.AllQuiet())
	}
}

// A page that shows twelve of eighty-one and says nothing has hidden
// sixty-nine problems. This is the guard against that.
func TestBuildNamespaces_WithheldNamespacesAreCountedAndStillShown(t *testing.T) {
	var remediations []v1alpha1.Remediation
	for i := range 40 {
		ns := fmt.Sprintf("bad-%03d", i)
		// Volume decides the order once the failure counts tie, so vary it.
		for j := 0; j <= i%5; j++ {
			remediations = append(remediations,
				in(failedRemediation(fmt.Sprintf("f-%03d-%d", i, j), i+j), ns, "api"))
		}
	}
	for i := range 10 {
		remediations = append(remediations,
			in(succeededRemediation(fmt.Sprintf("ok-%03d", i), i), fmt.Sprintf("fine-%03d", i), "api"))
	}

	view := buildNamespaces(remediations, Posture{}, testNow(), NamespaceFilter{})

	if view.Attention != 40 {
		t.Fatalf("Attention = %d, want 40 — the count is of every namespace "+
			"with something wrong, not of the ones that fitted", view.Attention)
	}
	if len(view.Rows) != attentionLimit {
		t.Errorf("cards = %d, want %d", len(view.Rows), attentionLimit)
	}
	if view.Withheld != 40-attentionLimit {
		t.Errorf("Withheld = %d, want %d", view.Withheld, 40-attentionLimit)
	}

	// Nothing may vanish: every namespace is on the page somewhere.
	if got := len(view.Rows) + len(view.Rest); got != 50 {
		t.Errorf("cards + table rows = %d, want 50 — a namespace was dropped", got)
	}

	// The withheld ones open the table, ahead of the namespaces with nothing
	// wrong, so the order still reads worst-first across the whole page.
	for i := range view.Withheld {
		if !view.Rest[i].NeedsAttention() {
			t.Fatalf("table row %d does not need attention, but %d withheld "+
				"namespaces should come first", i, view.Withheld)
		}
	}

	// And the page says so in words rather than truncating quietly.
	if !strings.Contains(view.RestNote(), "did not fit") {
		t.Errorf("RestNote() = %q, want it to say what was withheld", view.RestNote())
	}
	if !strings.Contains(view.Summary(), "have something wrong") {
		t.Errorf("Summary() = %q, want it to count what needs attention", view.Summary())
	}
	if view.RestTitle() == "Quiet" {
		t.Error(`RestTitle() = "Quiet" for a table that opens with failures`)
	}
}

// The posture chip is today's configuration; the counts are history. The
// project's own rule is that a record carries the posture it ran under and a
// later config change cannot rewrite it — so the two can legitimately
// disagree, and a page that showed the chip beside the counts without saying
// so would be contradicting itself in silence.
func TestBuildNamespaces_APostureChangeIsVisible(t *testing.T) {
	ranLive := in(succeededRemediation("ranLive", 10), "prod", "api")
	ranLive.Spec.DryRun = false
	dry := in(simulatedRemediation("dry", "", 5), "prod", "api")
	dry.Spec.DryRun = true

	// Reporting today, but something ran for ranLive before that.
	reporting := buildNamespaces([]v1alpha1.Remediation{ranLive, dry},
		Posture{DryRun: true}, testNow(), NamespaceFilter{})
	row := reporting.Rest[0]
	if row.Posture != "Reporting" {
		t.Fatalf("Posture = %q, want Reporting", row.Posture)
	}
	if row.RanForReal != 1 || row.RanDry != 1 {
		t.Errorf("RanForReal=%d RanDry=%d, want 1 and 1", row.RanForReal, row.RanDry)
	}
	if !strings.Contains(row.PostureNote, "ran live") {
		t.Errorf("PostureNote = %q, want it to count what ran live", row.PostureNote)
	}
	// The pattern is stated once, where the eye lands, rather than repeated on
	// every row of a cluster whose posture has moved.
	if reporting.Shifted != 1 {
		t.Errorf("Shifted = %d, want 1", reporting.Shifted)
	}
	if !strings.Contains(reporting.Summary(), "different posture") {
		t.Errorf("Summary() = %q, want it to explain the mismatch once",
			reporting.Summary())
	}

	// Live today, but nothing has actually run yet — worth saying, because a
	// reader would otherwise assume the successes were ranLive ones.
	live := buildNamespaces([]v1alpha1.Remediation{dry},
		Posture{DryRun: false}, testNow(), NamespaceFilter{})
	if got := live.Rest[0].PostureNote; !strings.Contains(got, "only reported") { //nolint:goconst
		t.Errorf("PostureNote = %q, want it to say nothing has run for ranLive yet", got)
	}

	// And when they agree, the page stays quiet about it.
	agreed := buildNamespaces([]v1alpha1.Remediation{ranLive},
		Posture{DryRun: false}, testNow(), NamespaceFilter{})
	if got := agreed.Rest[0].PostureNote; got != "" {
		t.Errorf("PostureNote = %q, want empty when the records agree with the posture", got)
	}
}

// A hundred and fifty rows ordered by severity is the right default and the
// wrong only option: "how is payments doing" was a question this page could
// answer and could not be asked.
func TestNamespaces_Filters(t *testing.T) {
	quiet := in(succeededRemediation("ok", 10), "quiet", "api")

	loud := in(failedRemediation("bad", 9), "loud", "api")
	loud.Status.Escalation = &v1alpha1.EscalationStatus{Phase: v1alpha1.StepPhaseSucceeded}

	silent := in(failedRemediation("worse", 8), "silent", "api")

	records := []v1alpha1.Remediation{quiet, loud, silent}
	posture := Posture{DryRun: true, Live: []string{"loud"}}

	all := buildNamespaces(records, posture, testNow(), NamespaceFilter{})
	if all.Total != 3 || all.Shown != 3 {
		t.Fatalf("unfiltered: total %d, shown %d; want 3 and 3", all.Total, all.Shown)
	}

	tests := []struct {
		name   string
		filter NamespaceFilter
		want   []string
	}{
		{name: "one namespace", filter: NamespaceFilter{Name: "quiet"}, want: []string{"quiet"}},
		{name: "acting only", filter: NamespaceFilter{Posture: PostureLive}, want: []string{"loud"}},
		{
			name:   "reporting only",
			filter: NamespaceFilter{Posture: PostureReporting},
			want:   []string{"quiet", "silent"},
		},
		{
			// The slice worth reaching first: a failure nobody was told
			// about. "loud" failed but its escalation got through.
			name:   "nobody was told",
			filter: NamespaceFilter{Show: ShowUnheard},
			want:   []string{"silent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := buildNamespaces(records, posture, testNow(), tt.filter)

			var got []string
			for _, row := range append(append([]NamespaceRow{}, view.Rows...), view.Rest...) {
				got = append(got, row.Name)
			}
			sort.Strings(got)
			if len(got) != len(tt.want) {
				t.Fatalf("rows = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("rows = %v, want %v", got, tt.want)
				}
			}
			if view.Shown != len(tt.want) {
				t.Errorf("Shown = %d, want %d", view.Shown, len(tt.want))
			}

			// The counts beside the controls are of the cluster, not of what
			// survives — a control whose own numbers move as it is used
			// cannot be reasoned about.
			if view.Total != all.Total || view.Attention != all.Attention {
				t.Errorf("the totals moved with the filter: total %d, attention %d",
					view.Total, view.Attention)
			}
			if len(view.Names) != all.Total {
				t.Errorf("the select offers %d namespaces, want all %d",
					len(view.Names), all.Total)
			}
		})
	}
}

// Choosing a namespace must not silently drop the posture already chosen.
func TestNamespaceFilter_LinksKeepTheOtherClauses(t *testing.T) {
	active := NamespaceFilter{Name: "payments", Posture: PostureLive, Show: ShowUnheard}

	path := active.Path()
	for _, want := range []string{"ns=payments", "posture=live", "show=unheard"} {
		if !strings.Contains(path, want) {
			t.Errorf("Path() = %q, want it to carry %q", path, want)
		}
	}

	groups := namespaceGroups(active, namespaceCounts{attention: 2, unheard: 1, live: 3, reporting: 4})
	for _, group := range groups {
		for _, option := range group.Options {
			if !strings.Contains(option.URL, "ns=payments") {
				t.Errorf("%s option %q links to %q, which dropped the namespace",
					group.Label, option.Label, option.URL)
			}
		}
	}
}
