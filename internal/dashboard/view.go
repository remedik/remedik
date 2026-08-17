package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

// The view models every page shares, and the primitives that order and narrow
// the records they are built from.
//
// Building views here, rather than reaching into the API types from the
// templates, keeps two promises: the templates hold no logic worth testing,
// and everything worth testing is a pure function from resources to a struct.
//
// A page's own view lives with that page — overview.go, remediations.go,
// remediation.go, namespaces.go, strategies.go. This file holds only what more
// than one of them needs, which is what stops it growing back into the
// nine-hundred-line file it was.

// Navigation identifiers, used to mark the current page in the header.
const (
	navNone         = ""
	navOverview     = "overview"
	navRemediations = "remediations"
	navNamespaces   = "namespaces"
	navStrategies   = "strategies"
)

// Tones map a state onto the palette. They are names, not colours: the
// stylesheet decides what "ok" looks like in light and in dark, and every
// tone is paired with a word so colour is never the only signal.
const (
	toneOK      = "ok"
	toneFailed  = "failed"
	toneDryRun  = "dryrun"
	toneRunning = "running"
	toneWaiting = "waiting"
	// toneWarn is a problem somebody already knows about, which is not the
	// same as one nobody was told about — the whole point of the namespaces
	// page is that those two do not look alike.
	toneWarn  = "warn"
	toneMuted = "muted"
)

// Posture is what remedik is allowed to do, as the pages need to say it.
//
// It is declared here rather than imported from internal/engine for the same
// reason engine.Snapshot is declared there: the dashboard renders, it does
// not depend on the engine, and the conversion happens once in main.
type Posture struct {
	// DryRun is the default: true when a namespace with no override only
	// reports.
	DryRun bool
	// Live and DryRunOnly are the namespaces that differ from the default,
	// sorted.
	Live       []string
	DryRunOnly []string
}

// Mixed reports whether any namespace differs from the default.
//
// A badge reading "Dry-run" over a cluster where two namespaces are live is
// the most misleading thing per-namespace posture could produce, so the
// pages ask this before they say anything about posture at all.
func (p Posture) Mixed() bool { return len(p.Live) > 0 || len(p.DryRunOnly) > 0 }

// Exceptions is the namespaces that differ, whichever way they differ.
func (p Posture) Exceptions() []string {
	if p.DryRun {
		return p.Live
	}
	return p.DryRunOnly
}

// postureChipLimit is how many namespaces the header chip names.
//
// It exists because the chip listed all of them, and a cluster with twenty
// exceptions turned the header into a paragraph that pushed the cluster name
// onto a second line — on every page, since this is the chrome. Three is
// enough to recognise, and the full list is still in the title attribute and
// on the overview's posture panel.
const postureChipLimit = 3

// ExceptionsBrief is the header chip's text: a few names, then a count.
func (p Posture) ExceptionsBrief() string {
	names := p.Exceptions()
	if len(names) <= postureChipLimit {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more",
		strings.Join(names[:postureChipLimit], ", "), len(names)-postureChipLimit)
}

// ExceptionsAll is every differing namespace, for the title attribute.
func (p Posture) ExceptionsAll() string { return strings.Join(p.Exceptions(), ", ") }

// Page carries what every page's chrome needs.
type Page struct {
	// Title is the browser title and the page heading.
	Title string
	// Nav marks the active navigation entry.
	Nav string
	// DryRun reports the operator's default posture.
	DryRun bool
	// Posture is the whole picture, including the namespaces that differ.
	Posture Posture
	// Namespace is where the records being shown live.
	Namespace string
	// Cluster names the cluster, when the operator was given a name. Empty
	// hides the chip entirely rather than showing a placeholder.
	Cluster string
	// Version is the operator build.
	Version string
	// Asset fingerprints the stylesheet and script, so an upgraded operator
	// is never read through a cached copy of the old one.
	Asset string
	// RenderedAt is when this page was produced.
	RenderedAt string
}

// Label is one key/value pair — an alert label, an action parameter or a
// trigger matcher.
type Label struct {
	Key   string
	Value string
}

// Stat is one figure on the overview.
//
// Every figure links to the list it counts. A number somebody cannot click
// is a number they have to go and look for, which on a dashboard is the same
// as not having it.
type Stat struct {
	Label  string
	Value  string
	Detail string
	Tone   string
	URL    string
}

// RemediationRow is one execution in a list.
type RemediationRow struct {
	Name     string
	URL      string
	Strategy string
	Target   string
	Alert    string
	State    string
	Tone     string
	Age      string
	AgeExact string
	Duration string
	Attempt  int32
	DryRun   bool
	Reason   string
	// Escalated is "sent" or "failed" when this remediation ran an
	// escalation, and empty otherwise. It rides beside the state rather
	// than in a column of its own: most rows would have nothing in it, and
	// the one that matters — a failure whose page did not go out — needs to
	// be loud in the list, not discoverable one click away.
	Escalated string
}

// Escalation markers, as the list renders them.
const (
	escalationSent   = "sent"
	escalationFailed = "failed"
)

// DryRunReport is the summary an operator shows their team before turning
// dry-run off: how much would have happened, over how long, and by which
// strategy.
type DryRunReport struct {
	// Active reports whether dry-run is on now. A report can also be shown
	// after it was turned off, describing the trial that led to that.
	Active     bool
	Simulated  int
	Targets    int
	Since      string
	SinceAge   string
	Period     string
	ByStrategy []SimulatedTally
}

// SimulatedTally is one strategy's share of a dry-run trial.
type SimulatedTally struct {
	Strategy string
	Count    int
	Targets  int
	Share    int
	// Example is the plan line from the most recent simulation, which is
	// the literal answer to "what would this have done?".
	Example string
	LastRun string
}

// ErrorView is the page shown instead of a page.
//
// It carries the same chrome as any other, so a reader who hits a 404 or a
// read failure still knows which cluster they are looking at and can navigate
// away — an error page that drops the shell reads as the whole thing being
// broken rather than one request.
type ErrorView struct {
	Page

	// Status is the HTTP status, and Title and Detail say what happened in
	// words: what went wrong, and what to do about it.
	Status int
	Title  string
	Detail string
}

// --------------------------------------------------------------------------
// Builders
// --------------------------------------------------------------------------

// buildDryRunReport summarises the simulated records.
//
// It is built whenever dry-run is on — a trial that has produced nothing
// yet is itself worth stating — and whenever simulations exist, so the
// report an operator based their decision on survives turning dry-run off.
func buildDryRunReport(remediations []*v1alpha1.Remediation, dryRun bool, now time.Time) *DryRunReport {
	type tally struct {
		count   int
		targets map[string]struct{}
		example string
		last    time.Time
	}

	byStrategy := map[string]*tally{}
	targets := map[string]struct{}{}
	var simulated int
	var earliest time.Time

	// The list is newest first, so the first record seen for a strategy is
	// its most recent one — which is the example worth showing.
	for _, rem := range remediations {
		if rem.Status.State != v1alpha1.RemediationStateSimulated {
			continue
		}
		simulated++

		created := rem.CreationTimestamp.Time
		if earliest.IsZero() || created.Before(earliest) {
			earliest = created
		}
		if rem.Spec.Target != "" {
			targets[rem.Spec.Target] = struct{}{}
		}

		t, ok := byStrategy[rem.Spec.StrategyName]
		if !ok {
			t = &tally{targets: map[string]struct{}{}, example: firstPlan(rem), last: created}
			byStrategy[rem.Spec.StrategyName] = t
		}
		t.count++
		if rem.Spec.Target != "" {
			t.targets[rem.Spec.Target] = struct{}{}
		}
		if created.After(t.last) {
			t.last = created
		}
	}

	if !dryRun && simulated == 0 {
		return nil
	}

	report := &DryRunReport{
		Active:    dryRun,
		Simulated: simulated,
		Targets:   len(targets),
	}
	if !earliest.IsZero() {
		report.Since = FormatTimestamp(earliest)
		report.SinceAge = FormatAge(earliest, now)
		report.Period = FormatPeriod(now.Sub(earliest))
	}

	for name, t := range byStrategy {
		report.ByStrategy = append(report.ByStrategy, SimulatedTally{
			Strategy: name,
			Count:    t.count,
			Targets:  len(t.targets),
			Share:    percent(t.count, simulated),
			Example:  t.example,
			LastRun:  FormatAge(t.last, now),
		})
	}
	// Busiest first, then by name so the order is stable between refreshes
	// of the same data.
	sort.Slice(report.ByStrategy, func(i, j int) bool {
		if report.ByStrategy[i].Count != report.ByStrategy[j].Count {
			return report.ByStrategy[i].Count > report.ByStrategy[j].Count
		}
		return report.ByStrategy[i].Strategy < report.ByStrategy[j].Strategy
	})

	return report
}

func applyFilter(
	remediations []*v1alpha1.Remediation, filter Filter,
) []*v1alpha1.Remediation {
	if !filter.Active() {
		return remediations
	}
	// Sized from what matches rather than from the input, in two passes.
	// Counting is cheap; a slice grown to ten thousand capacity to hold sixty
	// entries is not.
	n := 0
	for _, rem := range remediations {
		if filter.Matches(rem) {
			n++
		}
	}
	kept := make([]*v1alpha1.Remediation, 0, n)
	for _, rem := range remediations {
		if filter.Matches(rem) {
			kept = append(kept, rem)
		}
	}
	return kept
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// newestFirst returns the records ordered the way an incident is read: what
// just happened, first. Ties break by name so two records created in the same
// second do not swap places between refreshes.
//
// It returns pointers and leaves the caller's slice alone, and both halves of
// that matter.
//
// A Remediation is 552 bytes, so sorting ten thousand of them by value moves
// five and a half megabytes around to answer a question about timestamps;
// sorting pointers moves eighty kilobytes. And the previous version sorted in
// place, which reordered the manager's cached list — harmless as it happened,
// but a view builder quietly rearranging its input is the kind of side effect
// that is only harmless until somebody calls it twice.
func newestFirst(remediations []v1alpha1.Remediation) []*v1alpha1.Remediation {
	out := make([]*v1alpha1.Remediation, len(remediations))
	for i := range remediations {
		out[i] = &remediations[i]
	}
	sortNewestFirst(out)
	return out
}

// sortNewestFirst orders an existing pointer slice in place.
func sortNewestFirst(remediations []*v1alpha1.Remediation) {
	sort.Slice(remediations, func(i, j int) bool {
		a, b := remediations[i].CreationTimestamp.Time, remediations[j].CreationTimestamp.Time
		if a.Equal(b) {
			return remediations[i].Name < remediations[j].Name
		}
		return a.After(b)
	})
}

// sortedLabels renders a map as a stable, sorted list. Map iteration order
// is random in Go, and a page whose rows move on every refresh is a page
// nobody trusts.
func sortedLabels(m map[string]string) []Label {
	if len(m) == 0 {
		return nil
	}
	labels := make([]Label, 0, len(m))
	for k, v := range m {
		labels = append(labels, Label{Key: k, Value: v})
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].Key < labels[j].Key })
	return labels
}
