package dashboard

// Ordering the list.
//
// A table header means "sort by this" to everybody who has ever used one, and
// this table's headers meant nothing: the order was newest-first and there was
// no way to ask for another. "Which of these took ten minutes" is a question
// an incident asks and the page could not answer.
//
// Like the filter, ordering is navigation. Every header is a link, so there is
// no state held between choosing and applying — which matters more here than
// anywhere else on this page, because the headers sit inside the region the
// ten-second refresh replaces. A control that held its own state there would
// lose it mid-use, which is the shape of the bug this page has already had
// twice.

import (
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

const (
	paramSort = "sort"
	paramDir  = "dir"
)

// Sort keys, named after the column they order.
const (
	SortAge      = "age"
	SortName     = "name"
	SortStrategy = "strategy"
	SortTarget   = "target"
	SortAlert    = "alert"
	SortState    = "state"
	SortTook     = "took"
)

// Sort is the order in force.
//
// The zero value is newest first, which is what the list did before headers
// could be clicked and what it still does with no query string.
type Sort struct {
	// Key is the column, or "" for the default.
	Key string
	// Desc reverses the column's natural direction.
	Desc bool
}

// sortable lists the keys a column may be ordered by, with the direction each
// starts in.
//
// Times and durations start at the largest, because "newest" and "slowest"
// are the questions somebody clicks those columns to ask. Words start at A,
// because that is what a sorted list of names means.
var sortable = map[string]bool{
	SortAge:      true,
	SortName:     true,
	SortStrategy: true,
	SortTarget:   true,
	SortAlert:    true,
	SortState:    true,
	SortTook:     true,
}

func descendsFirst(key string) bool { return key == SortAge || key == SortTook }

// ParseSort reads the order from a query string.
//
// An unknown key is the default order rather than an error, for the same
// reason an unknown filter value is kept: a mistyped parameter in a URL
// somebody pasted into a channel should give a wide answer, not a 400.
func ParseSort(query url.Values) Sort {
	key := strings.TrimSpace(query.Get(paramSort))
	if !sortable[key] {
		return Sort{}
	}

	s := Sort{Key: key, Desc: descendsFirst(key)}
	switch strings.TrimSpace(query.Get(paramDir)) {
	case "asc":
		s.Desc = false
	case "desc":
		s.Desc = true
	}
	return s
}

// Query renders the order back into query parameters, so the filter's links
// can carry the order and the headers can carry the filter.
func (s Sort) Query() url.Values {
	values := url.Values{}
	if s.Key == "" {
		return values
	}
	values.Set(paramSort, s.Key)
	if s.Desc {
		values.Set(paramDir, "desc")
	} else {
		values.Set(paramDir, "asc")
	}
	return values
}

// Active reports whether this is anything other than the default order.
func (s Sort) Active() bool { return s.Key != "" }

// Apply orders the records in place.
//
// Every comparison falls back to the creation time and then the name, so the
// order is total: two records that tie on the column being sorted keep a
// stable position between refreshes. A list that reshuffles under the reader
// is a list nobody trusts, and with a hundred rows of the same strategy that
// is most of the page.
func (s Sort) Apply(remediations []*v1alpha1.Remediation) {
	if s.Key == "" {
		sortNewestFirst(remediations)
		return
	}

	less := func(i, j int) bool {
		a, b := remediations[i], remediations[j]

		var ordered, tie bool
		switch s.Key {
		case SortName:
			ordered, tie = a.Name < b.Name, a.Name == b.Name
		case SortStrategy:
			ordered, tie = a.Spec.StrategyName < b.Spec.StrategyName,
				a.Spec.StrategyName == b.Spec.StrategyName
		case SortTarget:
			ordered, tie = a.Spec.Target < b.Spec.Target, a.Spec.Target == b.Spec.Target
		case SortAlert:
			ordered, tie = a.Spec.Alert.Name < b.Spec.Alert.Name,
				a.Spec.Alert.Name == b.Spec.Alert.Name
		case SortState:
			left, right := displayState(a.Status.State), displayState(b.Status.State)
			ordered, tie = left < right, left == right
		case SortTook:
			left, right := took(a), took(b)
			ordered, tie = left < right, left == right
		default: // SortAge
			left, right := a.CreationTimestamp.Time, b.CreationTimestamp.Time
			ordered, tie = left.Before(right), left.Equal(right)
		}

		if tie {
			// Newest first within a tie, then by name, so the tail of a long
			// run of equal values does not move between renders.
			if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
				return a.CreationTimestamp.After(b.CreationTimestamp.Time)
			}
			return a.Name < b.Name
		}
		if s.Desc {
			return !ordered
		}
		return ordered
	}

	sort.SliceStable(remediations, less)
}

// took is how long an execution ran, for ordering.
//
// A record that never ran has no duration, and sorts as zero rather than as
// something: the column shows it as an em dash for the same reason.
func took(rem *v1alpha1.Remediation) time.Duration {
	from, to := rem.Status.StartedAt, rem.Status.CompletedAt
	if from == nil || to == nil || from.IsZero() || to.IsZero() {
		return 0
	}
	if span := to.Sub(from.Time); span > 0 {
		return span
	}
	return 0
}

// Column is one header, as the link that orders by it.
type Column struct {
	// Label is what the header says.
	Label string
	// Key is the sort key, or "" for a column that cannot be ordered.
	Key string
	// URL orders by this column, flipping the direction when it is already
	// the one in force — the same rule the filter follows, so a header is
	// never a dead end.
	URL string
	// Sorted is "ascending", "descending" or "" and goes straight into
	// aria-sort, which is how a screen reader announces the order.
	Sorted string
	// Numeric right-aligns the column, for durations and ages.
	Numeric bool
}

// Columns builds the header row for a filter and an order.
func Columns(filter Filter, current Sort) []Column {
	specs := []struct {
		label   string
		key     string
		numeric bool
	}{
		{label: "Remediation", key: SortName},
		{label: "Strategy", key: SortStrategy},
		{label: "Target", key: SortTarget},
		{label: "Alert", key: SortAlert},
		{label: "State", key: SortState},
		{label: "Took", key: SortTook, numeric: true},
		{label: "Age", key: SortAge, numeric: true},
	}

	columns := make([]Column, 0, len(specs))
	for _, spec := range specs {
		next := Sort{Key: spec.key, Desc: descendsFirst(spec.key)}
		column := Column{Label: spec.label, Key: spec.key, Numeric: spec.numeric}

		if current.Key == spec.key {
			// Clicking the column already in force reverses it.
			next.Desc = !current.Desc
			if current.Desc {
				column.Sorted = "descending"
			} else {
				column.Sorted = "ascending"
			}
		}

		column.URL = sortedPath(filter, next)
		columns = append(columns, column)
	}
	return columns
}

// sortedPath is the list page carrying both a filter and an order.
func sortedPath(filter Filter, order Sort) string {
	values := url.Values{}
	for key, value := range filter.Values() {
		values[key] = value
	}
	for key, value := range order.Query() {
		values[key] = value
	}
	if len(values) == 0 {
		return remediationsPath
	}
	return remediationsPath + "?" + values.Encode()
}

// Describe is the order in words, for the table's caption — which is what a
// screen reader announces before the rows, and the one place the order is
// stated rather than shown.
func (s Sort) Describe() string {
	switch s.Key {
	case "":
		return "newest first"
	case SortAge:
		if s.Desc {
			return "newest first"
		}
		return "oldest first"
	case SortTook:
		if s.Desc {
			return "slowest first"
		}
		return "quickest first"
	case SortName:
		return withDirection("by name", s.Desc)
	case SortStrategy:
		return withDirection("by strategy", s.Desc)
	case SortTarget:
		return withDirection("by target", s.Desc)
	case SortAlert:
		return withDirection("by alert", s.Desc)
	case SortState:
		return withDirection("by state", s.Desc)
	default:
		return "newest first"
	}
}

func withDirection(what string, desc bool) string {
	if desc {
		return what + ", Z to A"
	}
	return what + ", A to Z"
}
