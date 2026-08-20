package dashboard

import (
	"net/url"
	"strings"
	"testing"

	"github.com/remedik/remedik/api/v1alpha1"
)

// repeated is a run of records that say exactly the same thing: one strategy,
// one target, one alert, one outcome, an hour apart. It is what a crash-loop
// nobody has fixed looks like in the list.
func repeated(n int) []v1alpha1.Remediation {
	out := make([]v1alpha1.Remediation, 0, n)
	for i := range n {
		out = append(out, failedRemediation("pod-crashloop-"+string(rune('a'+i)), (i+1)*60))
	}
	return out
}

func TestGrouping_CollapsesARunAndCountsIt(t *testing.T) {
	view := buildRemediations(repeated(8), Filter{}, Sort{}, 1, testNow())

	if len(view.Rows) != 1 {
		t.Fatalf("8 identical records drew %d lines, want 1", len(view.Rows))
	}
	line := view.Rows[0]
	if line.Count != 8 {
		t.Errorf("the line stands for %d records, want 8", line.Count)
	}
	if line.NewestAge != "1h" || line.OldestAge != "8h" {
		t.Errorf("run bounded as %s..%s, want 1h..8h", line.NewestAge, line.OldestAge)
	}

	// The records themselves are one click away, and the link selects exactly
	// them rather than approximately them.
	query, err := url.Parse(line.GroupURL)
	if err != nil {
		t.Fatalf("group URL %q: %v", line.GroupURL, err)
	}
	want := map[string]string{
		"strategy": "pod-crashloop",
		"target":   "deployment/payments/api",
		"alert":    "KubePodCrashLooping",
		"state":    "Failed",
	}
	for key, value := range want {
		if got := query.Query().Get(key); got != value {
			t.Errorf("group URL %s = %q, want %q", key, got, value)
		}
	}
}

// The figures above the table count records. A page that said "1-100 of 1328"
// over eleven visible lines would be lying in the one place a reader counts.
func TestGrouping_CountsStayAboutRecords(t *testing.T) {
	view := buildRemediations(repeated(8), Filter{}, Sort{}, 1, testNow())

	if view.Total != 8 || view.Shown != 8 {
		t.Errorf("total %d, shown %d, want 8 and 8", view.Total, view.Shown)
	}
	if view.Failed != 8 {
		t.Errorf("failed tally = %d, want 8", view.Failed)
	}
	if view.Collapsed != 7 {
		t.Errorf("collapsed = %d, want 7 records folded into their line", view.Collapsed)
	}
}

// A target that failed twice and then succeeded is three records and two
// facts. Collapsing the success into the failures would hide the only one
// worth reading.
func TestGrouping_StopsAtADifferentOutcome(t *testing.T) {
	records := repeated(2)
	success := failedRemediation("pod-crashloop-ok", 30)
	success.Status.State = v1alpha1.RemediationStateSucceeded
	records = append(records, success)

	view := buildRemediations(records, Filter{}, Sort{}, 1, testNow())

	if len(view.Rows) != 2 {
		t.Fatalf("drew %d lines, want the success apart from the failures", len(view.Rows))
	}
	if view.Rows[0].State != "Succeeded" || view.Rows[0].Count != 1 {
		t.Errorf("newest line is %s x%d, want the lone success",
			view.Rows[0].State, view.Rows[0].Count)
	}
	if view.Rows[1].Count != 2 {
		t.Errorf("the failures drew x%d, want x2", view.Rows[1].Count)
	}
}

// Ordered by duration, "adjacent" is an accident of the comparison, so a
// group formed from it would present an arbitrary subset as a run.
func TestGrouping_IsOffInAnyOtherOrder(t *testing.T) {
	records := repeated(8)

	byTime := buildRemediations(records, Filter{}, Sort{}, 1, testNow())
	if !byTime.Grouped {
		t.Error("the default order does not group")
	}

	byDuration := buildRemediations(records, Filter{}, Sort{Key: SortTook}, 1, testNow())
	if byDuration.Grouped {
		t.Error("ordering by duration still groups")
	}
	if len(byDuration.Rows) != 8 {
		t.Errorf("drew %d lines, want every record on its own", len(byDuration.Rows))
	}
	for _, line := range byDuration.Rows {
		if line.Repeats() {
			t.Fatalf("line %s stands for %d records in an ungrouped order",
				line.Name, line.Count)
		}
	}
}

// Turning the page dropped the order: page two of "slowest first" was page
// two of newest-first, which is a different set of rows presented as the
// continuation of the one being read.
func TestPaging_CarriesTheOrderAndTheFilter(t *testing.T) {
	records := make([]v1alpha1.Remediation, 0, pageSize+10)
	for i := range pageSize + 10 {
		records = append(records, succeededRemediation("run-"+string(rune('a'+i%26))+
			string(rune('a'+i/26)), i+1))
	}

	order := Sort{Key: SortTook, Desc: true}
	filter := Filter{State: "Succeeded"}
	view := buildRemediations(records, filter, order, 1, testNow())

	next := view.Paging.NextURL
	if next == "" {
		t.Fatal("no next page on a list longer than one page")
	}
	for _, want := range []string{"sort=took", "dir=desc", "state=Succeeded", "page=2"} {
		if !strings.Contains(next, want) {
			t.Errorf("next page %q does not carry %s", next, want)
		}
	}
}

// Changing the filter or the order returns to the first page: page seven of
// one question is not page seven of another.
func TestFilterAndSortLinksReturnToTheFirstPage(t *testing.T) {
	path := sortedPath(Filter{Namespace: "payments"}, Sort{Key: SortState})
	if strings.Contains(path, paramPage) {
		t.Errorf("a filter link carries a page number: %q", path)
	}
}
