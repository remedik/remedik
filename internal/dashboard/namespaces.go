package dashboard

import (
	"fmt"
	"sort"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

// The namespaces page answers "where is this going badly".
//
// It is deliberately not called health: remedik knows the remediations it
// ran, not whether the workloads in a namespace are well. A page that
// implied otherwise would be a dashboard being authoritative about something
// it never measured, which is the failure this project spends most of its
// care avoiding.
//
// So every column here is remedik's own record: how often it acted, how that
// went, whether anybody was told, and under which posture — because a
// namespace where remedik only reports and one where it acts are not
// comparable, and showing their failure counts side by side without saying
// which is which invites exactly the wrong conclusion.

// NamespacesView is the /namespaces page.
type NamespacesView struct {
	Page

	// Rows is one namespace each, ordered by what needs attention.
	Rows []NamespaceRow
	// Total is every namespace remedik has touched.
	Total int
	// Executions is every record behind the page.
	Executions int
	// Attention is how many rows have something wrong.
	Attention int
}

// Any reports whether anything has been recorded at all.
func (v NamespacesView) Any() bool { return len(v.Rows) > 0 }

// NamespaceRow is one namespace's record.
type NamespaceRow struct {
	Name string

	// Posture is "Live" or "Reporting" — what remedik may do here.
	Posture string
	// PostureTone is the palette name for the posture chip.
	PostureTone string
	// PostureDetail explains the chip in a few words.
	PostureDetail string

	// Total, Succeeded, Failed, Simulated and InFlight are the outcomes.
	Total     int
	Succeeded int
	Failed    int
	Simulated int
	InFlight  int

	// Unheard is failures nobody was told about: no escalation ran, or the
	// escalation itself failed. This is the number that matters most, and
	// the reason the page exists rather than a namespace column on a list.
	Unheard int

	// Rate is the success rate over attempts that actually ran, in words.
	Rate string
	// RatePct is that rate as a number for the inline bar, 0-100, and -1
	// when nothing ran for real.
	RatePct int

	// Last is when this namespace last did anything, in words.
	Last string

	// Tone is the row's overall reading.
	Tone string
	// Note is one sentence about the row, when there is something to say.
	Note string

	// URL is this namespace's executions.
	URL string
}

// NeedsAttention reports whether this row is why somebody opened the page.
func (r NamespaceRow) NeedsAttention() bool { return r.Failed > 0 || r.Unheard > 0 }

func buildNamespaces(
	remediations []v1alpha1.Remediation,
	posture Posture,
	now time.Time,
) NamespacesView {
	byName := make(map[string]*NamespaceRow)
	latest := make(map[string]time.Time)

	for i := range remediations {
		rem := &remediations[i]
		name := targetNamespaceOf(rem)
		if name == "" {
			// A record whose target names no namespace is a cluster-scoped
			// one — a node action. It belongs on the list page, not here,
			// where every row is a namespace.
			continue
		}

		row := byName[name]
		if row == nil {
			row = &NamespaceRow{Name: name, URL: byNamespace(name)}
			byName[name] = row
		}

		row.Total++
		switch rem.Status.State {
		case v1alpha1.RemediationStateSucceeded:
			row.Succeeded++
		case v1alpha1.RemediationStateFailed:
			row.Failed++
			if !wasHeard(rem) {
				row.Unheard++
			}
		case v1alpha1.RemediationStateSimulated:
			row.Simulated++
		default:
			row.InFlight++
		}

		when := rem.CreationTimestamp.Time
		if when.After(latest[name]) {
			latest[name] = when
			row.Last = FormatAge(when, now)
		}
	}

	view := NamespacesView{Total: len(byName)}
	for name, row := range byName {
		row.applyPosture(posture, name)
		row.summarise()
		view.Executions += row.Total
		if row.NeedsAttention() {
			view.Attention++
		}
		view.Rows = append(view.Rows, *row)
	}

	sortNamespaceRows(view.Rows)
	return view
}

// applyPosture says what remedik may do in this namespace.
func (r *NamespaceRow) applyPosture(posture Posture, name string) {
	dryRun := posture.DryRun
	for _, ns := range posture.Live {
		if ns == name {
			dryRun = false
		}
	}
	for _, ns := range posture.DryRunOnly {
		if ns == name {
			dryRun = true
		}
	}

	if dryRun {
		r.Posture = "Reporting"
		r.PostureTone = toneDryRun
		r.PostureDetail = "remedik plans here, it does not act"
		return
	}
	r.Posture = "Live"
	r.PostureTone = toneOK
	r.PostureDetail = "remedik acts here"
}

// summarise fills in the rate, the tone and the note.
func (r *NamespaceRow) summarise() {
	ran := r.Succeeded + r.Failed
	if ran == 0 {
		r.Rate = "nothing ran for real"
		r.RatePct = -1
	} else {
		r.RatePct = r.Succeeded * 100 / ran
		r.Rate = fmt.Sprintf("%d%% of %s", r.RatePct, plural(ran, "attempt-attempts"))
	}

	switch {
	case r.Unheard > 0:
		r.Tone = toneFailed
		r.Note = fmt.Sprintf("%s nobody was told about",
			plural(r.Unheard, "failure-failures"))
	case r.Failed > 0:
		r.Tone = toneWarn
		r.Note = fmt.Sprintf("%s, escalated", plural(r.Failed, "failure-failures"))
	case r.Simulated == r.Total && r.Total > 0:
		r.Tone = toneDryRun
		r.Note = "only ever reported"
	case r.Succeeded > 0:
		r.Tone = toneOK
		r.Note = "no failures recorded"
	default:
		r.Tone = toneMuted
	}
}

// sortNamespaceRows puts what needs attention first.
//
// Alphabetical is the ordering that requires reading everything. Failures
// nobody heard about outrank failures somebody has already seen, which
// outrank volume; the name breaks ties so the order does not shuffle between
// refreshes.
func sortNamespaceRows(rows []NamespaceRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Unheard != b.Unheard {
			return a.Unheard > b.Unheard
		}
		if a.Failed != b.Failed {
			return a.Failed > b.Failed
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return a.Name < b.Name
	})
}

// wasHeard reports whether somebody was told about this failure.
//
// A failure with no escalation configured is one nobody was told about, and
// so is one whose escalation itself failed. The distinction the page draws
// is not "was an escalation declared" but "did the message leave".
func wasHeard(rem *v1alpha1.Remediation) bool {
	return rem.Status.Escalation != nil &&
		rem.Status.Escalation.Phase == v1alpha1.StepPhaseSucceeded
}
