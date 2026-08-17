package dashboard

import (
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
	}, Posture{}, testNow())

	if view.Total != 2 {
		t.Fatalf("Total = %d, want 2 namespaces", view.Total)
	}
	if len(view.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(view.Rows))
	}
	if view.Executions != 3 {
		t.Errorf("Executions = %d, want 3", view.Executions)
	}
	if view.Rows[0].Name != "payments" || view.Rows[0].Total != 2 {
		t.Errorf("first row = %s with %d, want payments with 2",
			view.Rows[0].Name, view.Rows[0].Total)
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

	view := buildNamespaces(remediations, Posture{}, testNow())

	got := []string{view.Rows[0].Name, view.Rows[1].Name, view.Rows[2].Name}
	want := []string{"silent", "quiet", "busy"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v — a failure nobody heard about "+
				"must outrank one somebody has seen, which outranks volume",
				got, want)
		}
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
		Posture{}, testNow())

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

	view := buildNamespaces([]v1alpha1.Remediation{told}, Posture{}, testNow())

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
	}, posture, testNow())

	byName := map[string]NamespaceRow{}
	for _, row := range view.Rows {
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
	}, posture, testNow())

	for _, row := range view.Rows {
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
	}, Posture{DryRun: true}, testNow())

	row := view.Rows[0]
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
	}, Posture{}, testNow())

	if view.Total != 1 {
		t.Fatalf("Total = %d, want 1 — a node action is not a namespace", view.Total)
	}
	if view.Rows[0].Name != "payments" {
		t.Errorf("row = %q, want payments", view.Rows[0].Name)
	}
}

func TestBuildNamespaces_EmptyIsNotAnError(t *testing.T) {
	view := buildNamespaces(nil, Posture{}, testNow())

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

	first := buildNamespaces(remediations, Posture{}, testNow())
	for range 20 {
		again := buildNamespaces(remediations, Posture{}, testNow())
		for i := range first.Rows {
			if first.Rows[i].Name != again.Rows[i].Name {
				t.Fatalf("order changed between builds: %s then %s",
					first.Rows[i].Name, again.Rows[i].Name)
			}
		}
	}

	// Equal on every count, so the name decides.
	want := []string{"alpha", "mid", "zeta"}
	for i, name := range want {
		if first.Rows[i].Name != name {
			t.Errorf("row %d = %s, want %s", i, first.Rows[i].Name, name)
		}
	}
}

func TestBuildNamespaces_LastActivityIsTheNewestRecord(t *testing.T) {
	old := in(succeededRemediation("old", 600), "payments", "api")
	recent := in(succeededRemediation("recent", 3), "payments", "api")

	view := buildNamespaces([]v1alpha1.Remediation{old, recent}, Posture{}, testNow())

	if got := view.Rows[0].Last; got != "3m" {
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
	}, Posture{}, testNow())

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

	view := buildNamespaces([]v1alpha1.Remediation{bare}, Posture{}, testNow())

	if view.Total != 1 {
		t.Fatalf("Total = %d, want 1", view.Total)
	}
	if view.Rows[0].InFlight != 1 {
		t.Errorf("InFlight = %d, want 1 — an empty state has not been picked up yet",
			view.Rows[0].InFlight)
	}
}
