package dashboard

import (
	"net/url"
	"strconv"
	"time"

	"github.com/ratyx/remedik/api/v1alpha1"
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

	// Rows are the executions that survive the filter, newest first.
	Rows []RemediationRow
	// Total is how many survive it, and TotalUnfiltered how many exist.
	Total           int
	TotalUnfiltered int
	// Shown is how many are drawn on this page.
	Shown int

	// Filter is what is in force, and Groups are the controls.
	Filter Filter
	Groups []FilterGroup
	// Paging is where the reader is in the filtered set.
	Paging Paging

	// Breakdown counts the states within the filtered set, so the reader can
	// see the shape of what they asked for without counting rows.
	Succeeded int
	Failed    int
	Simulated int
	InFlight  int
}

// Filtered reports whether the page is showing a subset.
func (v RemediationsView) Filtered() bool { return v.Filter.Active() }

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
	remediations []v1alpha1.Remediation, filter Filter, page int, now time.Time,
) RemediationsView {
	sortNewestFirst(remediations)

	// The controls are built from every record, so a choice can always be
	// changed or undone. A control whose options shrink as you use it is one
	// you can get stuck in.
	options := BuildFilterOptions(remediations)

	view := RemediationsView{
		Filter:          filter,
		Groups:          options.Groups(filter, remediations),
		TotalUnfiltered: len(remediations),
	}

	kept := applyFilter(remediations, filter)
	counts := tally(kept)

	view.Total = len(kept)
	view.Succeeded = counts.succeeded
	view.Failed = counts.failed
	view.Simulated = counts.simulated
	view.InFlight = counts.inFlight

	view.Paging = pageOf(view.Total, page, filter)

	// Sliced from the page number rather than from Paging.First and .Last,
	// which are display values: an empty result sets First to 0 to render
	// "0 of 0", and deriving the loop from it started at index -1.
	start := min((view.Paging.Page-1)*pageSize, len(kept))
	for i := start; i < min(start+pageSize, len(kept)); i++ {
		view.Rows = append(view.Rows, buildRow(&kept[i], now))
	}
	view.Shown = len(view.Rows)

	return view
}

// pageOf works out which slice to draw and the links either side.
//
// A page number out of range is clamped rather than refused: a bookmarked
// "?page=40" on a list that has since been pruned to three pages should show
// the last page, not an error.
func pageOf(total, page int, filter Filter) Paging {
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
		paging.PrevURL = pageURL(filter, page-1)
	}
	if page < pages {
		paging.NextURL = pageURL(filter, page+1)
	}
	return paging
}

// pageURL keeps the filter and changes only the page, so paging and
// filtering compose instead of clearing each other.
func pageURL(filter Filter, page int) string {
	path := filter.Path()
	if page <= 1 {
		return path
	}
	separator := "?"
	if filter.Active() {
		separator = "&"
	}
	return path + separator + paramPage + "=" + strconv.Itoa(page)
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
