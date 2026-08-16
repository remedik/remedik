package dashboard

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/ratyx/remedik/api/v1alpha1"
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

	got := BuildFilterOptions(remediations)

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
	options := BuildFilterOptions([]v1alpha1.Remediation{
		{
			Spec:   v1alpha1.RemediationSpec{Target: "deployment/payments/api", StrategyName: "restart-api"},
			Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateSucceeded},
		},
		{
			Spec:   v1alpha1.RemediationSpec{Target: "deployment/payments/api", StrategyName: "restart-api"},
			Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateSucceeded},
		},
	})

	if options.Any() {
		t.Error("Any() = true although every record shares one namespace, strategy and state")
	}
}

// The counts above the table must describe the rows in it. Numbers that
// disagreed with the list below them would be worse than no filter at all.
func TestBuildOverview_StatsFollowTheFilter(t *testing.T) {
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

	all := buildOverview(remediations, nil, false, Filter{}, testNow())
	if all.Total != 3 || all.Filtered() {
		t.Fatalf("unfiltered: Total = %d, Filtered = %v", all.Total, all.Filtered())
	}

	only := buildOverview(remediations, nil, false, Filter{Namespace: "checkout"}, testNow())

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
	for _, stat := range only.Stats {
		if stat.Label == "Failed" && stat.Value != "0" {
			t.Errorf("Failed = %s, want 0: the failure is in another namespace", stat.Value)
		}
	}

	// The choices stay complete, so a selection can always be undone.
	if len(only.Options.Namespaces) != 2 {
		t.Errorf("Options.Namespaces = %v, want both namespaces even while filtered",
			only.Options.Namespaces)
	}
}

// Each clause must be liftable on its own, keeping the others: a reader who
// narrowed twice and wants to widen once should not have to start over.
func TestFilter_ChipsRemoveOneClauseAtATime(t *testing.T) {
	chips := Filter{Namespace: "payments", State: "Failed"}.Chips()

	if len(chips) != 2 {
		t.Fatalf("got %d chips, want 2", len(chips))
	}
	if chips[0].Label != "namespace" || chips[0].Value != "payments" {
		t.Errorf("first chip = %+v", chips[0])
	}
	if chips[0].RemoveURL != "/?state=Failed" {
		t.Errorf("removing the namespace gave %q, want the state kept", chips[0].RemoveURL)
	}
	if chips[1].RemoveURL != "/?namespace=payments" {
		t.Errorf("removing the state gave %q, want the namespace kept", chips[1].RemoveURL)
	}
}

func TestFilter_ChipsAreEmptyWhenNothingIsFiltered(t *testing.T) {
	if chips := (Filter{}).Chips(); len(chips) != 0 {
		t.Errorf("an inactive filter produced %d chips", len(chips))
	}
}

// The last chip removed must land on an unfiltered page, not on "/?".
func TestFilter_TheLastChipClearsEverything(t *testing.T) {
	chips := Filter{Strategy: "restart-api"}.Chips()
	if len(chips) != 1 {
		t.Fatalf("got %d chips, want 1", len(chips))
	}
	if chips[0].RemoveURL != "/" {
		t.Errorf("RemoveURL = %q, want %q", chips[0].RemoveURL, "/")
	}
}
