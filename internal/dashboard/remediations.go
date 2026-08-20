package dashboard

import (
	"net/url"
	"strconv"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

// paramPage is the page number, 1-based.
const paramPage = "page"

// RemediationsView is the list: every execution kept, the filters, and the
// counts that describe what is being shown.
//
// It is a page of its own because "is anything wrong right now?" and "what
// happened to payments last Tuesday?" want different layouts, and the front
// page trying to answer both answered neither well.
type RemediationsView struct {
	Page

	// Rows are the lines drawn. A line is one execution, or a run of
	// executions that say the same thing — see RemediationGroup.
	Rows []RemediationGroup
	// Total is how many survive the filter, and TotalUnfiltered how many
	// exist.
	Total           int
	TotalUnfiltered int
	// Shown is how many records this page covers, which is not len(Rows)
	// once repeats are collapsed. The figures a reader checks against the
	// table count records; the table says how many each line stands for.
	Shown int
	// Grouped reports that repeats were collapsed, and Collapsed how many
	// records that removed from the page. Both are stated rather than left
	// to be noticed: a row standing for twelve is a different object from a
	// row standing for one.
	Grouped   bool
	Collapsed int

	// Filter is what is in force, and Groups are the controls.
	Filter Filter
	Groups []FilterGroup
	// Sort is the order in force, and Columns are the headers that set it.
	Sort    Sort
	Columns []Column
	// Clauses are the narrowings that have no control of their own — a
	// target or an alert somebody clicked in the table. Without them the
	// page would be filtered by something it never shows, and the only way
	// out would be to clear everything.
	Clauses []AppliedClause
	// Paging is where the reader is in the filtered set.
	Paging Paging

	// Breakdown counts the states within the filtered set, so the reader can
	// see the shape of what they asked for without counting rows.
	Succeeded int
	Failed    int
	Simulated int
	InFlight  int
}

// AppliedClause is one such narrowing, with the link that lifts it.
type AppliedClause struct {
	// Label names the dimension, in the words the table's column uses.
	Label string
	// Value is what it was narrowed to.
	Value string
	// RemoveURL is the same view without this clause.
	RemoveURL string
	// Mono asks for the monospace treatment, because a target is an
	// identifier and an alertname is a word.
	Mono bool
}

// appliedClauses lists the narrowings that no control offers.
func appliedClauses(f Filter, order Sort) []AppliedClause {
	var out []AppliedClause
	if f.Target != "" {
		without := f
		without.Target = ""
		out = append(out, AppliedClause{
			Label: "Target", Value: f.Target, RemoveURL: sortedPath(without, order), Mono: true,
		})
	}
	if f.Alert != "" {
		without := f
		without.Alert = ""
		out = append(out, AppliedClause{
			Label: "Alert", Value: f.Alert, RemoveURL: sortedPath(without, order),
		})
	}
	return out
}

// Filtered reports whether the page is showing a subset.
func (v RemediationsView) Filtered() bool { return v.Filter.Active() }

// Ordering is the order in words, for the table's caption.
func (v RemediationsView) Ordering() string { return v.Sort.Describe() }

// Excluded is how many records the filter is hiding.
func (v RemediationsView) Excluded() int { return v.TotalUnfiltered - v.Total }

// HasRows reports whether anything survived the filter.
func (v RemediationsView) HasRows() bool { return v.Total > 0 }

// Empty reports the cluster having no records at all, which is a different
// situation from a filter matching none and needs a different answer.
func (v RemediationsView) Empty() bool { return v.TotalUnfiltered == 0 }

// Paging is which slice of the filtered set is drawn, and the links either
// side of it.
//
// A page that drew two hundred rows and said nine thousand eight hundred
// were not drawn was not a list of what happened; it was a truncation with
// an apology. Pages are links, so they compose with the filters, survive a
// refresh, and can be sent to somebody.
type Paging struct {
	// Page is the 1-based page being shown, and Pages how many there are.
	Page  int
	Pages int
	// First and Last are the 1-based positions of the rows drawn, for
	// "showing 101-200 of 9,800".
	First int
	Last  int
	// PrevURL and NextURL are empty at the ends.
	PrevURL string
	NextURL string
}

// Many reports whether there is more than one page.
func (p Paging) Many() bool { return p.Pages > 1 }

func buildRemediations(
	remediations []v1alpha1.Remediation, filter Filter, order Sort, page int, now time.Time,
) RemediationsView {
	ordered := newestFirst(remediations)

	// The controls are built from every record, so a choice can always be
	// changed or undone. A control whose options shrink as you use it is one
	// you can get stuck in.
	options := BuildFilterOptions(ordered)

	view := RemediationsView{
		Filter:          filter,
		Sort:            order,
		Groups:          options.Groups(filter, order, ordered),
		Clauses:         appliedClauses(filter, order),
		Columns:         Columns(filter, order),
		TotalUnfiltered: len(ordered),
	}

	kept := applyFilter(ordered, filter)
	// After the filter and before the page: the reader asked for this order
	// over what they asked to see, and page two has to be the second page of
	// that order rather than of another one.
	order.Apply(kept)
	counts := tally(kept)

	view.Total = len(kept)
	view.Succeeded = counts.succeeded
	view.Failed = counts.failed
	view.Simulated = counts.simulated
	view.InFlight = counts.inFlight

	view.Paging = pageOf(view.Total, page, filter, order)

	// Sliced from the page number rather than from Paging.First and .Last,
	// which are display values: an empty result sets First to 0 to render
	// "0 of 0", and deriving the loop from it started at index -1.
	start := min((view.Paging.Page-1)*pageSize, len(kept))
	drawn := kept[start:min(start+pageSize, len(kept))]

	view.Shown = len(drawn)
	view.Grouped = order.GroupsAdjacent()
	view.Rows = buildLines(drawn, now, filter, order, view.Grouped)
	view.Collapsed = view.Shown - len(view.Rows)

	return view
}

// RemediationGroup is one line of the list: an execution, or a run of
// executions that say the same thing.
//
// Eight consecutive rows reading pod-crashloop / KubePodCrashLooping / Failed
// are one fact printed eight times, at the top of the page, during the
// incident that fact is about. The second distinct thing that happened was
// below the fold.
type RemediationGroup struct {
	// The line drawn is the first record of the run, in the order in force.
	RemediationRow

	// Count is how many records this line stands for. One, for most.
	Count int
	// NewestAge and OldestAge bound the run in time, whichever direction the
	// list is ordered in.
	NewestAge string
	OldestAge string
	// GroupURL is the list narrowed to exactly this run, which is where the
	// records themselves are. Expansion is navigation for the same reason
	// filtering is: anything that held the open state would sit inside the
	// region the ten-second refresh replaces, and would be closed again by a
	// timer while somebody read it.
	GroupURL string
}

// Repeats reports whether this line stands for more than one record.
func (g RemediationGroup) Repeats() bool { return g.Count > 1 }

// buildLines renders a page of records, collapsing runs of identical ones
// when the order makes adjacency mean something.
//
// One pass, no map, no second query: two records are the same fact only if
// they are neighbours, which is exactly what the ordering already decided.
func buildLines(
	page []*v1alpha1.Remediation, now time.Time, base Filter, order Sort, group bool,
) []RemediationGroup {
	lines := make([]RemediationGroup, 0, len(page))

	for i := 0; i < len(page); {
		run := i + 1
		if group {
			for run < len(page) && sameFact(page[i], page[run]) {
				run++
			}
		}

		line := RemediationGroup{
			RemediationRow: buildRow(page[i], now, base, order),
			Count:          run - i,
		}
		if line.Repeats() {
			newest, oldest := span(page[i:run])
			line.NewestAge = FormatAge(newest, now)
			line.OldestAge = FormatAge(oldest, now)
			line.GroupURL = listPath(runFilter(base, page[i]), order, 1)
		}

		lines = append(lines, line)
		i = run
	}
	return lines
}

// sameFact reports whether two records would print the same line.
//
// The state is part of it: a target that failed twice and then succeeded is
// three records and two facts, and collapsing the success into the failures
// would hide the only one worth reading.
func sameFact(a, b *v1alpha1.Remediation) bool {
	return a.Spec.StrategyName == b.Spec.StrategyName &&
		a.Spec.Target == b.Spec.Target &&
		a.Spec.Alert.Name == b.Spec.Alert.Name &&
		a.Status.State == b.Status.State
}

// span is the newest and oldest creation time in a run, which is not the
// first and last: the list can be ordered oldest-first.
func span(run []*v1alpha1.Remediation) (newest, oldest time.Time) {
	newest, oldest = run[0].CreationTimestamp.Time, run[0].CreationTimestamp.Time
	for _, rem := range run[1:] {
		when := rem.CreationTimestamp.Time
		if when.After(newest) {
			newest = when
		}
		if when.Before(oldest) {
			oldest = when
		}
	}
	return newest, oldest
}

// runFilter narrows to exactly the records a group stands for, on top of
// whatever the reader was already looking through.
func runFilter(base Filter, rem *v1alpha1.Remediation) Filter {
	narrowed := base
	narrowed.Strategy = rem.Spec.StrategyName
	narrowed.Target = rem.Spec.Target
	narrowed.Alert = rem.Spec.Alert.Name
	narrowed.State = displayState(rem.Status.State)
	return narrowed
}

// pageOf works out which slice to draw and the links either side.
//
// A page number out of range is clamped rather than refused: a bookmarked
// "?page=40" on a list that has since been pruned to three pages should show
// the last page, not an error.
func pageOf(total, page int, filter Filter, order Sort) Paging {
	pages := (total + pageSize - 1) / pageSize
	if pages < 1 {
		pages = 1
	}
	page = min(max(page, 1), pages)

	paging := Paging{
		Page:  page,
		Pages: pages,
		First: (page-1)*pageSize + 1,
		Last:  min(page*pageSize, total),
	}
	if total == 0 {
		paging.First = 0
	}
	if page > 1 {
		paging.PrevURL = listPath(filter, order, page-1)
	}
	if page < pages {
		paging.NextURL = listPath(filter, order, page+1)
	}
	return paging
}

// listPath is the list carrying a filter, an order and a page — the one
// place any of the three is turned into a URL.
//
// It exists because the previous pageURL built its own from the filter alone,
// so turning the page silently dropped the order somebody had chosen: page
// two of "slowest first" was page two of newest-first. Every link on the page
// goes through here now, which is what stops the next dimension doing it
// again.
func listPath(filter Filter, order Sort, page int) string {
	values := filter.Values()
	for key, value := range order.Query() {
		values[key] = value
	}
	if page > 1 {
		values.Set(paramPage, strconv.Itoa(page))
	}
	if len(values) == 0 {
		return remediationsPath
	}
	return remediationsPath + "?" + values.Encode()
}

// ParsePage reads the page number. Anything unreadable is page one, for the
// same reason an unknown filter value is honoured: a URL from an incident
// channel must not become an error page.
func ParsePage(query url.Values) int {
	page, err := strconv.Atoi(query.Get(paramPage))
	if err != nil || page < 1 {
		return 1
	}
	return page
}
