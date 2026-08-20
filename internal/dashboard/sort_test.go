package dashboard

import (
	"net/url"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

// The headers meant nothing before this: the list was newest-first and there
// was no way to ask for another order. "Which of these took ten minutes" is a
// question an incident asks.
func TestSort_OrdersByEachColumn(t *testing.T) {
	quick := failedRemediation("b-quick", 30)
	quick.Spec.StrategyName = "beta"
	quick.Status.StartedAt = ptrTime(testNow().Add(-30 * time.Minute))
	quick.Status.CompletedAt = ptrTime(testNow().Add(-30*time.Minute + time.Second))

	slow := succeededRemediation("a-slow", 10)
	slow.Spec.StrategyName = "alpha"
	slow.Status.StartedAt = ptrTime(testNow().Add(-10 * time.Minute))
	slow.Status.CompletedAt = ptrTime(testNow().Add(-10*time.Minute + 5*time.Minute))

	tests := []struct {
		name  string
		order Sort
		first string
	}{
		{name: "default is newest first", order: Sort{}, first: "a-slow"},
		{name: "oldest first", order: Sort{Key: SortAge}, first: "b-quick"},
		{name: "slowest first", order: Sort{Key: SortTook, Desc: true}, first: "a-slow"},
		{name: "quickest first", order: Sort{Key: SortTook}, first: "b-quick"},
		{name: "by name", order: Sort{Key: SortName}, first: "a-slow"},
		{name: "by name reversed", order: Sort{Key: SortName, Desc: true}, first: "b-quick"},
		{name: "by strategy", order: Sort{Key: SortStrategy}, first: "a-slow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := []*v1alpha1.Remediation{&quick, &slow}
			tt.order.Apply(records)
			if records[0].Name != tt.first {
				t.Errorf("first row = %s, want %s", records[0].Name, tt.first)
			}
		})
	}
}

// A list that reshuffles under the reader is a list nobody trusts, and with a
// hundred rows of one strategy every comparison is a tie.
func TestSort_TiesKeepAStableOrder(t *testing.T) {
	var records []*v1alpha1.Remediation
	for _, name := range []string{"c", "a", "b"} {
		rem := succeededRemediation(name, 10)
		rem.Spec.StrategyName = "same"
		records = append(records, &rem)
	}

	first := append([]*v1alpha1.Remediation(nil), records...)
	Sort{Key: SortStrategy}.Apply(first)

	second := append([]*v1alpha1.Remediation(nil), records...)
	Sort{Key: SortStrategy}.Apply(second)

	for i := range first {
		if first[i].Name != second[i].Name {
			t.Fatalf("the order moved between renders: %s then %s", first[i].Name, second[i].Name)
		}
	}
	// All three tie on the strategy and on the age, so the name decides.
	if first[0].Name != "a" {
		t.Errorf("first = %s, want a: ties fall back to the name", first[0].Name)
	}
}

// Clicking the column already in force reverses it, and every header carries
// the filter — otherwise sorting would silently widen what is on screen.
func TestColumns_ReverseAndCarryTheFilter(t *testing.T) {
	filter := Filter{Namespace: "payments", State: "Failed"}
	columns := Columns(filter, Sort{Key: SortTook, Desc: true})

	var took *Column
	for i := range columns {
		if columns[i].Key == SortTook {
			took = &columns[i]
		}
		if !strings.Contains(columns[i].URL, "namespace=payments") || !strings.Contains(columns[i].URL, "state=Failed") {
			t.Errorf("header %q links to %q, which has dropped the filter",
				columns[i].Label, columns[i].URL)
		}
	}
	if took == nil {
		t.Fatal("no Took column")
	}
	if took.Sorted != "descending" {
		t.Errorf("aria-sort = %q, want descending", took.Sorted)
	}
	if !strings.Contains(took.URL, "dir=asc") {
		t.Errorf("the column in force links to %q, want the reverse of itself", took.URL)
	}
}

// A mistyped parameter in a URL somebody pasted into a channel should give a
// wide answer, not a 400.
func TestParseSort_UnknownKeyIsTheDefault(t *testing.T) {
	if got := ParseSort(url.Values{"sort": {"colour"}}); got.Active() {
		t.Errorf("ParseSort(colour) = %+v, want the default order", got)
	}
	got := ParseSort(url.Values{"sort": {"took"}})
	if !got.Active() || !got.Desc {
		t.Errorf("ParseSort(took) = %+v, want slowest first", got)
	}
	if asc := ParseSort(url.Values{"sort": {"took"}, "dir": {"asc"}}); asc.Desc {
		t.Errorf("dir=asc was ignored: %+v", asc)
	}
}

func ptrTime(t time.Time) *metav1.Time {
	stamp := metav1.NewTime(t)
	return &stamp
}
