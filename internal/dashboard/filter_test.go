package dashboard

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/remedik/remedik/api/v1alpha1"
)

func TestTargetNamespace(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "a namespaced target", target: "deployment/payments/api", want: "payments"},
		{name: "a cluster-scoped target has none", target: "node/worker-1"},
		{name: "an unresolved target has none", target: ""},
		{name: "nonsense is not a namespace", target: "a/b/c/d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TargetNamespace(tt.target); got != tt.want {
				t.Errorf("TargetNamespace(%q) = %q, want %q", tt.target, got, tt.want)
			}
		})
	}
}

func TestFilter_Matches(t *testing.T) {
	rem := func(target, strategy string, state v1alpha1.RemediationState) *v1alpha1.Remediation {
		return &v1alpha1.Remediation{
			Spec:   v1alpha1.RemediationSpec{Target: target, StrategyName: strategy},
			Status: v1alpha1.RemediationStatus{State: state},
		}
	}
	subject := rem("deployment/payments/api", "restart-api", v1alpha1.RemediationStateFailed)

	tests := []struct {
		name   string
		filter Filter
		record *v1alpha1.Remediation
		want   bool
	}{
		{name: "the zero filter admits everything", record: subject, want: true},
		{
			name:   "namespace matches",
			filter: Filter{Namespace: "payments"},
			record: subject, want: true,
		},
		{
			name:   "namespace does not match",
			filter: Filter{Namespace: "checkout"},
			record: subject,
		},
		{
			name:   "strategy and state together",
			filter: Filter{Strategy: "restart-api", State: "Failed"},
			record: subject, want: true,
		},
		{
			name:   "every clause must hold",
			filter: Filter{Namespace: "payments", State: "Succeeded"},
			record: subject,
		},
		{
			// A node belongs to no namespace, so a namespace filter must not
			// quietly include it — "everything in payments" would then be a
			// list containing something that is not in payments.
			name:   "a cluster-scoped record is excluded by a namespace filter",
			filter: Filter{Namespace: "payments"},
			record: rem("node/worker-1", "drain", v1alpha1.RemediationStateSucceeded),
		},
		{
			// The reconciler has not reached it. It reads as Pending
			// everywhere else on the page, so it must filter as Pending too.
			name:   "an empty state filters as Pending",
			filter: Filter{State: "Pending"},
			record: rem("deployment/payments/api", "restart-api", ""),
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.Matches(tt.record); got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseFilter(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  Filter
	}{
		{name: "nothing"},
		{
			name:  "all three",
			query: "namespace=payments&strategy=restart-api&state=Failed",
			want:  Filter{Namespace: "payments", Strategy: "restart-api", State: "Failed"},
		},
		{
			name:  "whitespace is trimmed, so a copied URL still works",
			query: "namespace=%20payments%20",
			want:  Filter{Namespace: "payments"},
		},
		{
			// A namespace with no records is a legitimate question, and the
			// answer — "nothing happened there" — is information. A 400
			// would turn a shareable URL into a trap.
			name:  "an unknown value is kept, not rejected",
			query: "namespace=does-not-exist",
			want:  Filter{Namespace: "does-not-exist"},
		},
		{
			name:  "an unknown parameter is ignored",
			query: "cluster=east&namespace=payments",
			want:  Filter{Namespace: "payments"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery() error = %v", err)
			}
			if got := ParseFilter(values); got != tt.want {
				t.Errorf("ParseFilter(%q) = %+v, want %+v", tt.query, got, tt.want)
			}
		})
	}
}

func TestFilter_Query(t *testing.T) {
	if got := (Filter{}).Query(); got != "" {
		t.Errorf("an empty filter rendered %q, want no query string", got)
	}

	got := Filter{Namespace: "payments", State: "Failed"}.Query()
	want := "?namespace=payments&state=Failed"
	if got != want {
		t.Errorf("Query() = %q, want %q", got, want)
	}
}

func TestBuildFilterOptions(t *testing.T) {
	remediations := []v1alpha1.Remediation{
		{
			Spec:   v1alpha1.RemediationSpec{Target: "deployment/payments/api", StrategyName: "restart-api"},
			Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateFailed},
		},
		{
			Spec:   v1alpha1.RemediationSpec{Target: "deployment/checkout/web", StrategyName: "restart-api"},
			Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateSucceeded},
		},
		{
			// Cluster-scoped: contributes a strategy and a state, but no
			// namespace, because it is in none.
			Spec:   v1alpha1.RemediationSpec{Target: "node/worker-1", StrategyName: "drain"},
			Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateSucceeded},
		},
	}

	got := BuildFilterOptions(ptrs(remediations))

	if want := []string{"checkout", "payments"}; !reflect.DeepEqual(got.Namespaces, want) {
		t.Errorf("Namespaces = %v, want %v", got.Namespaces, want)
	}
	if want := []string{"drain", "restart-api"}; !reflect.DeepEqual(got.Strategies, want) {
		t.Errorf("Strategies = %v, want %v", got.Strategies, want)
	}
	if want := []string{"Failed", "Succeeded"}; !reflect.DeepEqual(got.States, want) {
		t.Errorf("States = %v, want %v", got.States, want)
	}
	if !got.Any() {
		t.Error("Any() = false although there is more than one of everything")
	}
}

// With one namespace and one strategy, a filter row is furniture.
func TestFilterOptions_AnyIsFalseWhenThereIsNothingToChooseBetween(t *testing.T) {
	options := BuildFilterOptions(ptrs([]v1alpha1.Remediation{
		{
			Spec:   v1alpha1.RemediationSpec{Target: "deployment/payments/api", StrategyName: "restart-api"},
			Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateSucceeded},
		},
		{
			Spec:   v1alpha1.RemediationSpec{Target: "deployment/payments/api", StrategyName: "restart-api"},
			Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateSucceeded},
		},
	}))

	if options.Any() {
		t.Error("Any() = true although every record shares one namespace, strategy and state")
	}
}

// The counts above the table must describe the rows in it. Numbers that
// disagreed with the list below them would be worse than no filter at all.
func TestBuildRemediations_CountsFollowTheFilter(t *testing.T) {
	// Targets set here rather than taken from the fixtures: this test is
	// about which namespace a record is in, so that must be visible in it.
	remediations := []v1alpha1.Remediation{
		succeededRemediation("ok-payments", 30),
		failedRemediation("bad-payments", 20),
		succeededRemediation("ok-checkout", 10),
	}
	remediations[0].Spec.Target = "deployment/payments/api"
	remediations[1].Spec.Target = "deployment/payments/api"
	remediations[2].Spec.Target = "deployment/checkout/web"

	all := buildRemediations(remediations, Filter{}, 1, testNow())
	if all.Total != 3 || all.Filtered() {
		t.Fatalf("unfiltered: Total = %d, Filtered = %v", all.Total, all.Filtered())
	}

	only := buildRemediations(remediations, Filter{Namespace: "checkout"}, 1, testNow())

	if only.Total != 1 {
		t.Errorf("Total = %d, want 1", only.Total)
	}
	if only.TotalUnfiltered != 3 {
		t.Errorf("TotalUnfiltered = %d, want 3", only.TotalUnfiltered)
	}
	if only.Excluded() != 2 {
		t.Errorf("Excluded() = %d, want 2", only.Excluded())
	}
	if !only.Filtered() {
		t.Error("Filtered() = false with a namespace selected")
	}
	if only.Failed != 0 {
		t.Errorf("Failed = %d, want 0: the failure is in another namespace", only.Failed)
	}
	if len(only.Rows) != 1 || only.Rows[0].Name != "ok-checkout" {
		t.Errorf("rows = %+v, want only the checkout record", only.Rows)
	}

	// The controls stay complete, so a selection can always be undone.
	namespaces := groupNamed(t, only.Groups, "Namespace")
	if len(namespaces.Options) != 2 {
		t.Errorf("namespace options = %+v, want both namespaces even while filtered",
			namespaces.Options)
	}
}

// Filtering is navigation: each option is a link, and the one in force links
// to the page without it, so the same control narrows and widens.
func TestFilterGroups_LinksBothNarrowAndWiden(t *testing.T) {
	remediations := []v1alpha1.Remediation{
		succeededRemediation("ok-payments", 30),
		succeededRemediation("ok-checkout", 10),
	}
	remediations[0].Spec.Target = "deployment/payments/api"
	remediations[1].Spec.Target = "deployment/checkout/web"

	view := buildRemediations(remediations, Filter{Namespace: "payments"}, 1, testNow())
	group := groupNamed(t, view.Groups, "Namespace")

	if group.AllURL != "/remediations" {
		t.Errorf("AllURL = %q, want the unfiltered list", group.AllURL)
	}
	if group.AllSelected {
		t.Error("AllSelected = true although a namespace is in force")
	}

	for _, option := range group.Options {
		switch option.Value {
		case "payments":
			if !option.Selected {
				t.Error("payments is not marked selected")
			}
			if option.URL != "/remediations" {
				t.Errorf("the selected option links to %q, want the page without it", option.URL)
			}
		case "checkout":
			if option.Selected {
				t.Error("checkout is marked selected")
			}
			if option.URL != "/remediations?namespace=checkout" {
				t.Errorf("an unselected option links to %q", option.URL)
			}
		}
		if option.Count != 1 {
			t.Errorf("%s counted %d, want 1", option.Value, option.Count)
		}
	}
}

// A dimension with one value is not a choice, and a row offering it is
// furniture on a page whose job is to be scanned.
func TestFilterGroups_OmitADimensionWithNothingToChoose(t *testing.T) {
	remediations := []v1alpha1.Remediation{
		succeededRemediation("ok-1", 30),
		succeededRemediation("ok-2", 10),
	}
	view := buildRemediations(remediations, Filter{}, 1, testNow())

	for _, group := range view.Groups {
		if len(group.Options) < 2 {
			t.Errorf("group %q offers %d option(s)", group.Label, len(group.Options))
		}
	}
}

func groupNamed(t *testing.T, groups []FilterGroup, label string) FilterGroup {
	t.Helper()
	for _, group := range groups {
		if group.Label == label {
			return group
		}
	}
	t.Fatalf("no %q filter group in %+v", label, groups)
	return FilterGroup{}
}

// TargetNamespace is on the hottest path in the package: every page asks it of
// every record several times over. It was rewritten to allocate nothing, so
// this pins the behaviour it had when it did.
func TestTargetNamespace_EveryShape(t *testing.T) {
	for target, want := range map[string]string{
		"deployment/payments/api":     "payments",
		"statefulset/data/ledger":     "data",
		"pod/kube-system/coredns-abc": "kube-system",
		// Cluster-scoped: two parts, so no namespace.
		"node/worker-3": "",
		// Not a target this understands.
		"a/b/c/d": "",
		"a/b/c/":  "",
		"/b/c":    "b",
		"a//c":    "",
		// Nothing at all.
		"":            "",
		"deployment":  "",
		"deployment/": "",
	} {
		if got := TargetNamespace(target); got != want {
			t.Errorf("TargetNamespace(%q) = %q, want %q", target, got, want)
		}
	}
}

func BenchmarkTargetNamespace(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = TargetNamespace("deployment/payments/api")
	}
}
