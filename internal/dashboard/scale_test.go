package dashboard

import (
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

// The sizes here are a mid-sized platform team, not an outlier. remedik is
// meant to be installed by anybody, so the dashboard's cost has to be a
// measured number rather than an impression — the first version of the
// filter counts read as obviously correct and benchmarked at 50ms.
const (
	scaleNamespaces = 150
	scaleStrategies = 40
	scaleRecords    = 10000
)

func bigCluster(namespaces, strategies, records int) []v1alpha1.Remediation {
	out := make([]v1alpha1.Remediation, 0, records)
	for i := range records {
		rem := succeededRemediation(fmt.Sprintf("rem-%05d", i), i%1440)
		rem.Spec.Target = fmt.Sprintf("deployment/ns-%03d/app", i%namespaces)
		rem.Spec.StrategyName = fmt.Sprintf("strategy-%02d", i%strategies)
		rem.CreationTimestamp = metav1.NewTime(testNow())
		out = append(out, rem)
	}
	return out
}

// A hundred and fifty namespaces as a wall of links is not something anybody
// filters by eye, so above a threshold the control becomes a select. This
// asserts the threshold is actually applied, because the wall is what the
// owner reported.
func TestFilterControlsStayUsableAtScale(t *testing.T) {
	records := bigCluster(scaleNamespaces, scaleStrategies, scaleRecords)
	view := buildRemediations(records, Filter{}, Sort{}, 1, testNow())

	byLabel := map[string]FilterGroup{}
	for _, group := range view.Groups {
		byLabel[group.Label] = group
	}

	namespaces := byLabel["Namespace"]
	if len(namespaces.Options) != scaleNamespaces {
		t.Fatalf("namespace options = %d, want every one of %d",
			len(namespaces.Options), scaleNamespaces)
	}
	if !namespaces.AsSelect {
		t.Errorf("%d namespaces render as links; that is a wall, not a control",
			len(namespaces.Options))
	}
	if len(namespaces.QuickPicks) > quickPickLimit+1 {
		t.Errorf("quick picks = %d, want at most %d", len(namespaces.QuickPicks), quickPickLimit+1)
	}

	// States are few, so they stay one-click.
	if states := byLabel["State"]; states.AsSelect {
		t.Error("the state row became a select although it has a handful of values")
	}

	// And the page is a page, not a truncation.
	if len(view.Rows) != pageSize {
		t.Errorf("rows drawn = %d, want a page of %d", len(view.Rows), pageSize)
	}
	if view.Paging.Pages != scaleRecords/pageSize {
		t.Errorf("pages = %d, want %d", view.Paging.Pages, scaleRecords/pageSize)
	}
}

// The counts must be the same as counting each option separately, which is
// what the slow version did. This is the test that makes the rewrite a
// rewrite of the arithmetic rather than a change to what is shown.
func TestFilterCountsMatchCountingEachOptionSeparately(t *testing.T) {
	records := bigCluster(6, 4, 400)
	active := Filter{State: "Succeeded"}

	pointers := ptrs(records)
	options := BuildFilterOptions(pointers)
	groups := options.Groups(active, Sort{}, pointers)

	for _, group := range groups {
		rest := Filter{Namespace: active.Namespace, Strategy: active.Strategy, State: active.State}
		switch group.Param {
		case paramNamespace:
			rest.Namespace = ""
		case paramStrategy:
			rest.Strategy = ""
		case paramState:
			rest.State = ""
		}

		for _, option := range group.Options {
			want := 0
			probe := rest
			switch group.Param {
			case paramNamespace:
				probe.Namespace = option.Value
			case paramStrategy:
				probe.Strategy = option.Value
			case paramState:
				probe.State = option.Value
			}
			for i := range records {
				if probe.Matches(&records[i]) {
					want++
				}
			}
			if option.Count != want {
				t.Errorf("%s=%s counted %d, want %d", group.Param, option.Value, option.Count, want)
			}
		}
	}
}

// Paging composes with filtering rather than clearing it: a reader who
// narrowed to a namespace and turned the page must still be in it.
func TestPagingKeepsTheFilter(t *testing.T) {
	// Six pages' worth across two namespaces, so page two of ns-000 has a
	// page on either side of it.
	records := bigCluster(2, 2, pageSize*6)
	filter := Filter{Namespace: "ns-000"}

	view := buildRemediations(records, filter, Sort{}, 2, testNow())

	if view.Paging.Pages != 3 {
		t.Fatalf("pages = %d, want 3", view.Paging.Pages)
	}

	if !strings.Contains(view.Paging.NextURL, "namespace=ns-000") {
		t.Errorf("next page = %q, want the namespace kept", view.Paging.NextURL)
	}
	if !strings.Contains(view.Paging.PrevURL, "namespace=ns-000") {
		t.Errorf("previous page = %q, want the namespace kept", view.Paging.PrevURL)
	}
	for _, row := range view.Rows {
		if !strings.Contains(row.Target, "ns-000") {
			t.Fatalf("page two leaked a row from outside the filter: %s", row.Target)
		}
	}
}

func BenchmarkListPageAtScale(b *testing.B) {
	records := bigCluster(scaleNamespaces, scaleStrategies, scaleRecords)
	b.ResetTimer()
	for range b.N {
		_ = buildRemediations(records, Filter{}, Sort{}, 1, testNow())
	}
}

func BenchmarkOverviewAtScale(b *testing.B) {
	records := bigCluster(scaleNamespaces, scaleStrategies, scaleRecords)
	b.ResetTimer()
	for range b.N {
		_ = buildOverview(records, nil, Posture{}, testNow())
	}
}

// An empty result is the case that panicked: with nothing matching, the
// display's "0 of 0" made the row loop start at index -1, and the dashboard
// returned an empty reply rather than a page.
func TestPagingHandlesAnEmptyResult(t *testing.T) {
	for _, page := range []int{1, 2, 99} {
		view := buildRemediations(
			[]v1alpha1.Remediation{succeededRemediation("ok-1", 5)},
			Filter{Namespace: "no-such-namespace"}, Sort{}, page, testNow())

		if len(view.Rows) != 0 {
			t.Errorf("page %d: rows = %d, want none", page, len(view.Rows))
		}
		if view.Paging.Pages != 1 {
			t.Errorf("page %d: pages = %d, want 1", page, view.Paging.Pages)
		}
		if view.Paging.PrevURL != "" || view.Paging.NextURL != "" {
			t.Errorf("page %d: an empty result offers page links", page)
		}
	}
}

// The last page of an exact multiple is full, and the one after it is empty
// rather than negative.
func TestPagingAtTheBoundaries(t *testing.T) {
	records := bigCluster(1, 1, pageSize*2)

	last := buildRemediations(records, Filter{}, Sort{}, 2, testNow())
	if len(last.Rows) != pageSize {
		t.Errorf("the last page has %d rows, want a full page", len(last.Rows))
	}
	if last.Paging.NextURL != "" {
		t.Errorf("the last page offers a next page: %q", last.Paging.NextURL)
	}

	beyond := buildRemediations(records, Filter{}, Sort{}, 9, testNow())
	if beyond.Paging.Page != 2 {
		t.Errorf("page 9 of 2 = %d, want it clamped to 2", beyond.Paging.Page)
	}
}
