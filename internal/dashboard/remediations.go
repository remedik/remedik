package dashboard

import (
	"time"

	"github.com/ratyx/remedik/api/v1alpha1"
)

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
	// Shown is how many are drawn, and Hidden how many were left off the
	// end. A page that silently truncates is a page that lies about what
	// happened.
	Shown  int
	Hidden int

	// Filter is what is in force, and Groups are the controls.
	Filter Filter
	Groups []FilterGroup

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

func buildRemediations(
	remediations []v1alpha1.Remediation, filter Filter, now time.Time,
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

	view.Rows = make([]RemediationRow, 0, min(len(kept), listLimit))
	for i := range kept {
		if i == listLimit {
			break
		}
		view.Rows = append(view.Rows, buildRow(&kept[i], now))
	}
	view.Shown = len(view.Rows)
	view.Hidden = view.Total - view.Shown

	return view
}
