package dashboard

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
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

// The words a row's posture chip uses. They are what the filter compares
// against, so the label and the clause cannot drift apart.
const (
	postureLiveLabel      = "Live"
	postureReportingLabel = "Reporting"
)

// attentionLimit is how many namespaces get a full card.
//
// The number exists because the first version had no bound, and a cluster of
// a hundred and fifty namespaces with an ordinary failure rate put eighty-one
// of them above the fold — at which point "needs attention" has stopped
// meaning anything and the page is a hundred and fifty kilobytes of cards
// nobody reads.
//
// A dozen is about what somebody will actually work through. Everything past
// it is in the table, with its failures still shown, and the page says how
// many there are rather than quietly truncating.
const attentionLimit = 12

// NamespacesView is the /namespaces page.
//
// It is two lists rather than one, and that is the answer to scale. Paging a
// list ordered by severity would be worse than either: page two of "what
// needs attention" is by construction the part that does not. So the worst
// handful get a card each, and every other namespace is a row in a compact
// table that still carries its numbers.
type NamespacesView struct {
	Page

	// Rows is the worst namespaces, worst first, at most attentionLimit.
	Rows []NamespaceRow
	// Rest is every other namespace, busiest first. It is not "the quiet
	// ones": some of them have failures, and the table shows them.
	Rest []NamespaceRow
	// Total is every namespace remedik has touched.
	Total int
	// Executions is every record behind the page.
	Executions int
	// Attention is how many namespaces have something wrong, whether or not
	// they fitted on a card.
	Attention int
	// Withheld is how many of those did not fit, so the page can say so
	// rather than truncating in silence.
	Withheld int
	// Shifted is how many namespaces hold executions that ran under a
	// different posture from the one configured now. Counted so the summary
	// can state the pattern once instead of every row repeating it.
	Shifted int

	// Filter is what is in force, and Groups are the controls.
	//
	// A hundred and fifty rows ordered by severity is the right default and
	// the wrong only option: "how is payments doing" was a question this
	// page could answer and could not be asked, so it was answered by
	// scrolling.
	Filter NamespaceFilter
	Groups []NamespaceGroup
	// Names is every namespace on the page, for the select's type-ahead.
	Names []string
	// Shown is how many namespaces survive the filter.
	Shown int
}

// NamespaceFilter narrows the page.
type NamespaceFilter struct {
	// Name is one namespace, chosen from the select.
	Name string
	// Posture is "live" or "dryRun" — what remedik may do there now.
	Posture string
	// Show is "attention" or "unheard".
	Show string
}

// Namespace filter values, in the query string.
const (
	paramNamespaceName = "ns"
	paramPosture       = "posture"
	paramShow          = "show"

	// ShowAttention keeps the namespaces with something wrong.
	ShowAttention = "attention"
	// ShowUnheard keeps the ones holding failures nobody was told about,
	// which is the slice worth reaching first and the hardest to spot.
	ShowUnheard = "unheard"

	// PostureLive and PostureReporting are what a row's posture may be.
	PostureLive      = "live"
	PostureReporting = "dryRun"
)

// ParseNamespaceFilter reads the filter from a query string. An unknown value
// is kept for the name and dropped for the closed sets, for the same reason
// the record filter keeps one: a pasted URL should answer widely rather than
// fail.
func ParseNamespaceFilter(query url.Values) NamespaceFilter {
	f := NamespaceFilter{Name: strings.TrimSpace(query.Get(paramNamespaceName))}

	switch strings.TrimSpace(query.Get(paramPosture)) {
	case PostureLive:
		f.Posture = PostureLive
	case PostureReporting:
		f.Posture = PostureReporting
	}
	switch strings.TrimSpace(query.Get(paramShow)) {
	case ShowAttention:
		f.Show = ShowAttention
	case ShowUnheard:
		f.Show = ShowUnheard
	}
	return f
}

// Active reports whether anything is being narrowed.
func (f NamespaceFilter) Active() bool {
	return f.Name != "" || f.Posture != "" || f.Show != ""
}

// Path is the page carrying this filter.
func (f NamespaceFilter) Path() string {
	values := url.Values{}
	for key, value := range map[string]string{
		paramNamespaceName: f.Name,
		paramPosture:       f.Posture,
		paramShow:          f.Show,
	} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if len(values) == 0 {
		return namespacesPath
	}
	return namespacesPath + "?" + values.Encode()
}

// keeps reports whether a row survives the filter.
func (f NamespaceFilter) keeps(row *NamespaceRow) bool {
	if f.Name != "" && row.Name != f.Name {
		return false
	}
	switch f.Posture {
	case PostureLive:
		if row.Posture != postureLiveLabel {
			return false
		}
	case PostureReporting:
		if row.Posture == postureLiveLabel {
			return false
		}
	}
	switch f.Show {
	case ShowAttention:
		return row.NeedsAttention()
	case ShowUnheard:
		return row.Unheard > 0
	}
	return true
}

// NamespaceGroup is one dimension of the filter, as the pills that set it.
type NamespaceGroup struct {
	Label   string
	Options []NamespaceOption
}

// NamespaceOption is one choice, as the link that applies or removes it.
type NamespaceOption struct {
	Label    string
	URL      string
	Selected bool
	// Count is how many namespaces carry this value, over the whole cluster
	// rather than over what is shown: a control whose own numbers move as
	// you use it cannot be reasoned about.
	Count int
	// Countless marks the "All" option, which needs no number beside the
	// total the page already states.
	Countless bool
}

// AnyRest reports whether the compact table has anything in it.
func (v NamespacesView) AnyRest() bool { return len(v.Rest) > 0 }

// RestTitle names the table for what is actually in it. When the attention
// list was capped, the table is not "the quiet ones" — it opens with the
// namespaces that did not fit, and calling it quiet would be a lie the page
// tells to look tidier.
func (v NamespacesView) RestTitle() string {
	switch {
	case v.Withheld > 0:
		return "Everything else"
	case v.Attention > 0:
		return "Quiet"
	default:
		return "Every namespace"
	}
}

// RestNote says what the second group contains, including what was held back
// from the first. A page that silently shows twelve of eighty-one is a page
// that has hidden sixty-nine problems.
func (v NamespacesView) RestNote() string {
	if v.Withheld > 0 {
		return fmt.Sprintf("%s with failures that did not fit above, then %s with none",
			plural(v.Withheld, "namespace-namespaces"),
			plural(len(v.Rest)-v.Withheld, "namespace-namespaces"))
	}
	return "no failures on record, busiest first"
}

// Summary is the one line above the table.
//
// It replaces two paragraphs of hint text that between them repeated the
// counts already in the badges and the group headers. On a page whose problem
// was that it had too much on it, prose that says what the numbers say is the
// first thing to go.
func (v NamespacesView) Summary() string {
	const absence = "Namespaces remedik has never touched do not appear — " +
		"their absence is the answer."
	shifted := ""
	if v.Shifted > 0 {
		shifted = fmt.Sprintf(" %s hold executions that ran under a different "+
			"posture from the one configured now, which is why a count can look "+
			"impossible beside its chip.", plural(v.Shifted, "namespace-namespaces"))
	}
	if v.Attention == 0 {
		return fmt.Sprintf("%s, none of them with a failure on record.%s %s",
			plural(v.Total, "namespace-namespaces"), shifted, absence)
	}
	return fmt.Sprintf("%s of %d have something wrong.%s %s",
		plural(v.Attention, "namespace-namespaces"), v.Total, shifted, absence)
}

// AllQuiet reports the good case: remedik has run here and nothing is wrong.
func (v NamespacesView) AllQuiet() bool { return v.Total > 0 && v.Attention == 0 }

// Any reports whether anything has been recorded at all.
func (v NamespacesView) Any() bool { return v.Total > 0 }

// AttentionLabel is the badge's text. It is built here rather than in the
// template because "1 need attention" is the kind of thing a reader notices
// and a template cannot conjugate.
func (v NamespacesView) AttentionLabel() string {
	if v.Attention == 1 {
		return "1 needs attention"
	}
	return fmt.Sprintf("%d need attention", v.Attention)
}

// NamespaceRow is one namespace's record.
type NamespaceRow struct {
	Name string

	// Posture is "Live" or "Reporting" — what remedik may do here *now*.
	Posture string
	// PostureTone is the palette name for the posture chip.
	PostureTone string
	// PostureDetail explains the chip in a few words.
	PostureDetail string
	// PostureNote is set when the records disagree with the posture as it is
	// configured today.
	//
	// This matters because of the project's own rule: the posture is resolved
	// once, when a record is created, and written onto it — so history says
	// which posture it ran under and a later config change cannot rewrite
	// it. Which means a namespace marked "Reporting" can perfectly well hold
	// executions that changed something, and a page showing today's chip
	// beside historical counts would be quietly contradicting itself.
	PostureNote string

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

	// RanForReal and RanDry count how the executions were actually recorded,
	// which is what makes a posture change visible.
	RanForReal int
	RanDry     int

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
	filter NamespaceFilter,
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
		if rem.Spec.DryRun {
			row.RanDry++
		} else {
			row.RanForReal++
		}
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
	var wrong, fine []NamespaceRow
	// Two passes, deliberately. The first is over every namespace, because
	// the totals the page states and the counts beside the filter's own
	// controls are about the cluster — a control whose numbers move as you
	// use it is one nobody can reason about. The second keeps what survives.
	var counts namespaceCounts
	for name, row := range byName {
		row.applyPosture(posture, name)
		row.summarise()
		view.Executions += row.Total
		counts.add(row)
		view.Names = append(view.Names, name)

		if !filter.keeps(row) {
			continue
		}
		if row.NeedsAttention() {
			wrong = append(wrong, *row)
			continue
		}
		fine = append(fine, *row)
	}
	sort.Strings(view.Names)
	view.Filter = filter
	view.Groups = namespaceGroups(filter, counts)

	sortNamespaceRows(wrong)
	sortNamespaceRows(fine)

	for _, row := range append(wrong, fine...) {
		if row.PostureNote != "" {
			view.Shifted++
		}
	}

	view.Attention = counts.attention
	view.Shown = len(wrong) + len(fine)
	if len(wrong) > attentionLimit {
		view.Rows = wrong[:attentionLimit]
		view.Withheld = len(wrong) - attentionLimit
		// The ones that did not fit keep their place at the top of the table,
		// ahead of the namespaces with nothing wrong.
		view.Rest = append(view.Rest, wrong[attentionLimit:]...)
	} else {
		view.Rows = wrong
	}
	view.Rest = append(view.Rest, fine...)

	return view
}

// namespaceCounts is how many namespaces carry each filterable property,
// over the whole cluster.
type namespaceCounts struct {
	attention int
	unheard   int
	live      int
	reporting int
}

func (c *namespaceCounts) add(row *NamespaceRow) {
	if row.NeedsAttention() {
		c.attention++
	}
	if row.Unheard > 0 {
		c.unheard++
	}
	if row.Posture == postureLiveLabel {
		c.live++
	} else {
		c.reporting++
	}
}

// namespaceGroups builds the controls.
//
// Every option is a link, and choosing the one already in force removes it —
// the same rule the record list follows, so a control both narrows and
// widens and nothing is a dead end. An option nothing matches is left out
// rather than offered as a way to reach an empty page.
func namespaceGroups(active NamespaceFilter, counts namespaceCounts) []NamespaceGroup {
	option := func(label, value, param string, count int) NamespaceOption {
		chosen := active.Show
		if param == paramPosture {
			chosen = active.Posture
		}

		next := active
		if value == chosen {
			value = ""
		}
		if param == paramPosture {
			next.Posture = value
		} else {
			next.Show = value
		}

		return NamespaceOption{
			Label:    label,
			URL:      next.Path(),
			Selected: value != "" && value == chosen,
			Count:    count,
		}
	}

	clearShow, clearPosture := active, active
	clearShow.Show = ""
	clearPosture.Posture = ""

	show := NamespaceGroup{Label: "Show", Options: []NamespaceOption{{
		Label: "All", URL: clearShow.Path(), Selected: active.Show == "", Countless: true,
	}}}
	if counts.attention > 0 {
		show.Options = append(show.Options,
			option("Needs attention", ShowAttention, paramShow, counts.attention))
	}
	if counts.unheard > 0 {
		show.Options = append(show.Options,
			option("Nobody was told", ShowUnheard, paramShow, counts.unheard))
	}

	posture := NamespaceGroup{Label: "Posture", Options: []NamespaceOption{{
		Label: "All", URL: clearPosture.Path(), Selected: active.Posture == "", Countless: true,
	}}}
	if counts.live > 0 {
		posture.Options = append(posture.Options,
			option("Acting", PostureLive, paramPosture, counts.live))
	}
	if counts.reporting > 0 {
		posture.Options = append(posture.Options,
			option("Reporting only", PostureReporting, paramPosture, counts.reporting))
	}

	groups := make([]NamespaceGroup, 0, 2)
	// One value is not a choice, and a row offering it is furniture.
	if len(show.Options) > 1 {
		groups = append(groups, show)
	}
	if len(posture.Options) > 2 {
		groups = append(groups, posture)
	}
	return groups
}

// Filtered reports whether the page is showing a subset.
func (v NamespacesView) Filtered() bool { return v.Filter.Active() }

// ClearNameURL is this view without the namespace clause, so the chip that
// shows it can also lift it.
func (v NamespacesView) ClearNameURL() string {
	without := v.Filter
	without.Name = ""
	return without.Path()
}

// applyPosture says what remedik may do in this namespace.
func (r *NamespaceRow) applyPosture(posture Posture, name string) {
	// The kill switch overrides every namespace's setting, so a row must not
	// claim Live while nothing anywhere will act.
	if posture.Paused {
		r.Posture = "Paused"
		r.PostureTone = toneWarn
		r.PostureDetail = "remediation is paused; remedik only reports here"
		return
	}

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
		r.Posture = postureReportingLabel
		r.PostureTone = toneDryRun
		r.PostureDetail = "remedik plans here, it does not act"
		if r.RanForReal > 0 {
			// Terse, and in the posture column rather than under the name.
			// The long form repeated on every row on a cluster where the
			// posture had moved, and a note that appears everywhere is not a
			// note. The count it carries is the informative half.
			r.PostureNote = fmt.Sprintf("%d ran live", r.RanForReal)
		}
		return
	}
	r.Posture = postureLiveLabel
	r.PostureTone = toneOK
	r.PostureDetail = "remedik acts here"
	if r.RanDry > 0 && r.RanForReal == 0 {
		r.PostureNote = fmt.Sprintf("%d only reported", r.RanDry)
	}
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
