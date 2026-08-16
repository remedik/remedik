package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ratyx/remedik/api/v1alpha1"
)

// The overview answers one question: is anything wrong right now?
//
// It is built as panels, each of which is a claim with a link to its
// evidence. That shape is deliberate — adding "namespace health" or
// "approvals waiting" later is a struct, a builder and a template block,
// not a rearrangement of the page — and it is why the long list moved to
// /remediations, which answers a different question entirely.

// OverviewView is the front page.
type OverviewView struct {
	Page

	// Posture states what remedik is allowed to do, and where.
	PosturePanel PosturePanel
	// Attention is what a reader should look at now. Empty is the good case
	// and says so.
	Attention AttentionPanel
	// Stats are the headline counts over the whole of recorded history.
	Stats []Stat
	// Activity is the last day, one bar per hour.
	Activity ActivityPanel
	// Namespaces and Strategies are where remediation has been happening.
	Namespaces []Breakdown
	Strategies []Breakdown
	// Recent is a short tail, with a link to the full list.
	Recent []RemediationRow
	// Total is every record kept.
	Total int
	// DryRunning summarises a dry-run trial, when there is one to report.
	DryRunning *DryRunReport

	// StrategyCount and EnabledCount let the empty state say something
	// useful: "nothing has matched yet" and "there are no strategies" are
	// different problems with different fixes.
	StrategyCount int
	EnabledCount  int
}

// HasRecords reports whether anything has run.
func (v OverviewView) HasRecords() bool { return v.Total > 0 }

// PosturePanel is what remedik may do, said plainly.
type PosturePanel struct {
	// Headline is the one-line answer.
	Headline string
	// Detail explains it, naming namespaces where the posture is mixed.
	Detail string
	// Tone is the palette name.
	Tone string
	// Mixed reports whether the default describes the whole cluster.
	Mixed bool
	// Live and Reporting are the namespaces that differ from the default.
	Live      []string
	Reporting []string
}

// AttentionItem is one thing worth looking at, with the list that shows it.
type AttentionItem struct {
	Count int
	Label string
	// Detail says why it matters, in one sentence.
	Detail string
	Tone   string
	URL    string
}

// AttentionPanel is what needs a person.
//
// Ordered by how much silence each represents: a failed escalation means
// nobody was told, which outranks a failure somebody has already seen.
type AttentionPanel struct {
	Items []AttentionItem
}

// Any reports whether anything needs attention.
func (p AttentionPanel) Any() bool { return len(p.Items) > 0 }

// ActivityBar is one hour of the activity panel.
type ActivityBar struct {
	// Label is the hour, for the tooltip and the table view.
	Label string
	// Succeeded, Failed and Simulated are the counts in that hour.
	Succeeded int
	Failed    int
	Simulated int
	// Total is their sum.
	Total int
	// Percent is the bar's height against the busiest hour, 0-100.
	Percent int
}

// ActivityPanel is the last day, one bar per hour.
//
// Bars are divs with a height and a table of the same numbers underneath for
// anybody not reading them visually. A charting library to draw twenty-four
// rectangles would cost a bundler, a release artifact and a request leaving
// the cluster, which this page's own CSP forbids.
type ActivityPanel struct {
	Bars []ActivityBar
	// Busiest is the count the tallest bar represents, so the axis can be
	// labelled with a real number.
	Busiest int
	// Total is every execution in the window.
	Total int
	// Window describes the span in words.
	Window string
}

// Any reports whether anything happened in the window.
func (p ActivityPanel) Any() bool { return p.Total > 0 }

// Breakdown is one row of "where is this happening": a name, its counts, and
// the filtered list that shows them.
type Breakdown struct {
	Name      string
	Total     int
	Failed    int
	Simulated int
	// Share is this row's percentage of the largest row, for the inline bar.
	Share int
	URL   string
}

func buildOverview(
	remediations []v1alpha1.Remediation,
	strategies []v1alpha1.RemediationStrategy,
	posture Posture,
	now time.Time,
) OverviewView {
	sortNewestFirst(remediations)

	counts := tally(remediations)

	view := OverviewView{
		Total:         len(remediations),
		StrategyCount: len(strategies),
		PosturePanel:  buildPosturePanel(posture),
		Attention:     buildAttention(remediations),
		Activity:      buildActivity(remediations, now),
		Namespaces:    buildBreakdown(remediations, targetNamespaceOf, byNamespace),
		Strategies:    buildBreakdown(remediations, strategyOf, byStrategy),
	}
	for i := range strategies {
		if strategies[i].IsEnabled() {
			view.EnabledCount++
		}
	}

	view.Stats = []Stat{
		{
			Label:  "Executions",
			Value:  fmt.Sprint(len(remediations)),
			Detail: fmt.Sprintf("across %s", plural(view.StrategyCount, "strategy-strategies")),
			Tone:   toneMuted,
			URL:    remediationsPath,
		},
		{
			Label:  "Succeeded",
			Value:  fmt.Sprint(counts.succeeded),
			Detail: successRate(counts.succeeded, counts.failed),
			Tone:   toneOK,
			URL:    Filter{State: string(v1alpha1.RemediationStateSucceeded)}.Path(),
		},
		{
			Label:  "Failed",
			Value:  fmt.Sprint(counts.failed),
			Detail: failedDetail(counts.failed),
			Tone:   toneFailed,
			URL:    Filter{State: string(v1alpha1.RemediationStateFailed)}.Path(),
		},
		{
			Label:  "Simulated",
			Value:  fmt.Sprint(counts.simulated),
			Detail: "recorded, nothing changed",
			Tone:   toneDryRun,
			URL:    Filter{State: string(v1alpha1.RemediationStateSimulated)}.Path(),
		},
		{
			Label:  "In flight",
			Value:  fmt.Sprint(counts.inFlight),
			Detail: "pending or running",
			Tone:   toneRunning,
			URL:    Filter{State: string(v1alpha1.RemediationStatePending)}.Path(),
		},
	}

	view.Recent = make([]RemediationRow, 0, min(len(remediations), recentLimit))
	for i := range remediations {
		if i == recentLimit {
			break
		}
		view.Recent = append(view.Recent, buildRow(&remediations[i], now))
	}

	if report := buildDryRunReport(remediations, posture.DryRun, now); report != nil {
		view.DryRunning = report
	}
	return view
}

// stateCounts is the headline tally.
type stateCounts struct {
	succeeded, failed, simulated, inFlight int
}

func tally(remediations []v1alpha1.Remediation) stateCounts {
	var counts stateCounts
	for i := range remediations {
		switch remediations[i].Status.State {
		case v1alpha1.RemediationStateSucceeded:
			counts.succeeded++
		case v1alpha1.RemediationStateFailed:
			counts.failed++
		case v1alpha1.RemediationStateSimulated:
			counts.simulated++
		case v1alpha1.RemediationStatePending, v1alpha1.RemediationStateRunning:
			counts.inFlight++
		default:
			// An empty state is a record the reconciler has not picked up
			// yet, which is in flight by any reading an operator cares
			// about.
			counts.inFlight++
		}
	}
	return counts
}

func buildPosturePanel(posture Posture) PosturePanel {
	panel := PosturePanel{
		Mixed:     posture.Mixed(),
		Live:      posture.Live,
		Reporting: posture.DryRunOnly,
	}

	switch {
	case panel.Mixed && posture.DryRun:
		panel.Headline = "Mixed"
		panel.Detail = fmt.Sprintf(
			"Reporting only, except in %s, where remedik acts.", joinNames(posture.Live))
		panel.Tone = toneRunning
	case panel.Mixed:
		panel.Headline = "Mixed"
		panel.Detail = fmt.Sprintf(
			"Acting on matching alerts, except in %s, where remedik only reports.",
			joinNames(posture.DryRunOnly))
		panel.Tone = toneRunning
	case posture.DryRun:
		panel.Headline = "Dry-run"
		panel.Detail = "Alerts are matched and recorded. Nothing in the cluster is changed."
		panel.Tone = toneDryRun
	default:
		panel.Headline = "Live"
		panel.Detail = "Matching alerts are remediated."
		panel.Tone = toneOK
	}
	return panel
}

func buildAttention(remediations []v1alpha1.Remediation) AttentionPanel {
	var failed, unheard, untold, interrupted int

	for i := range remediations {
		rem := &remediations[i]
		if rem.Status.State != v1alpha1.RemediationStateFailed {
			continue
		}
		failed++

		switch {
		case rem.Status.Escalation == nil:
			untold++
		case rem.Status.Escalation.Phase != v1alpha1.StepPhaseSucceeded:
			unheard++
		}
		if rem.Status.Reason == v1alpha1.ReasonInterrupted {
			interrupted++
		}
	}

	var panel AttentionPanel
	failedURL := Filter{State: string(v1alpha1.RemediationStateFailed)}.Path()

	// Ordered by how much silence each represents.
	if unheard > 0 {
		panel.Items = append(panel.Items, AttentionItem{
			Count: unheard,
			Label: unit(unheard, "escalation-escalations") + " failed",
			Detail: "A remediation failed and the attempt to report it failed too. " +
				"Assume nobody was told.",
			Tone: toneFailed,
			URL:  failedURL,
		})
	}
	if untold > 0 {
		panel.Items = append(panel.Items, AttentionItem{
			Count: untold,
			Label: unit(untold, "failure-failures") + " with no escalation",
			Detail: "Their strategies declare no onFailure.steps, so no alert went " +
				"anywhere. Not a fault — but worth knowing before concluding it is quiet.",
			Tone: toneWaiting,
			URL:  failedURL,
		})
	}
	if interrupted > 0 {
		panel.Items = append(panel.Items, AttentionItem{
			Count:  interrupted,
			Label:  unit(interrupted, "execution-executions") + " interrupted",
			Detail: "The operator restarted mid-execution. They were failed, never resumed.",
			Tone:   toneWaiting,
			URL:    failedURL,
		})
	}
	if failed > 0 && len(panel.Items) == 0 {
		panel.Items = append(panel.Items, AttentionItem{
			Count:  failed,
			Label:  unit(failed, "remediation-remediations") + " failed",
			Detail: "Each was escalated successfully, so somebody was told.",
			Tone:   toneFailed,
			URL:    failedURL,
		})
	}
	return panel
}

func buildActivity(remediations []v1alpha1.Remediation, now time.Time) ActivityPanel {
	panel := ActivityPanel{
		Bars:   make([]ActivityBar, activityHours),
		Window: fmt.Sprintf("last %d hours", activityHours),
	}

	// Bucket by hour, oldest first, so the panel reads left to right like
	// every other timeline somebody has seen.
	start := now.Truncate(time.Hour).Add(-time.Duration(activityHours-1) * time.Hour)
	for i := range panel.Bars {
		panel.Bars[i].Label = start.Add(time.Duration(i) * time.Hour).Format("15:04")
	}

	for i := range remediations {
		rem := &remediations[i]
		created := rem.CreationTimestamp.Time
		if created.Before(start) {
			continue
		}
		bucket := int(created.Sub(start) / time.Hour)
		if bucket < 0 || bucket >= len(panel.Bars) {
			continue
		}

		bar := &panel.Bars[bucket]
		switch rem.Status.State {
		case v1alpha1.RemediationStateSucceeded:
			bar.Succeeded++
		case v1alpha1.RemediationStateFailed:
			bar.Failed++
		case v1alpha1.RemediationStateSimulated:
			bar.Simulated++
		}
		bar.Total++
		panel.Total++
		if bar.Total > panel.Busiest {
			panel.Busiest = bar.Total
		}
	}

	for i := range panel.Bars {
		if panel.Busiest > 0 {
			panel.Bars[i].Percent = panel.Bars[i].Total * 100 / panel.Busiest
		}
	}
	return panel
}

// breakdownLimit is how many rows "where is this happening" shows. Beyond
// that the panel stops being a summary; the list is one click away.
const breakdownLimit = 5

func targetNamespaceOf(rem *v1alpha1.Remediation) string {
	return TargetNamespace(rem.Spec.Target)
}

func strategyOf(rem *v1alpha1.Remediation) string { return rem.Spec.StrategyName }

func byNamespace(name string) string { return Filter{Namespace: name}.Path() }

func byStrategy(name string) string { return Filter{Strategy: name}.Path() }

func buildBreakdown(
	remediations []v1alpha1.Remediation,
	key func(*v1alpha1.Remediation) string,
	link func(string) string,
) []Breakdown {
	rows := map[string]*Breakdown{}

	for i := range remediations {
		rem := &remediations[i]
		name := key(rem)
		if name == "" {
			continue
		}
		row, ok := rows[name]
		if !ok {
			row = &Breakdown{Name: name, URL: link(name)}
			rows[name] = row
		}
		row.Total++
		switch rem.Status.State {
		case v1alpha1.RemediationStateFailed:
			row.Failed++
		case v1alpha1.RemediationStateSimulated:
			row.Simulated++
		}
	}

	out := make([]Breakdown, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	// Busiest first, then by name so the order is stable between refreshes —
	// a panel that reshuffles under the reader is a panel nobody trusts.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Name < out[j].Name
	})

	if len(out) > 0 {
		largest := out[0].Total
		for i := range out {
			out[i].Share = out[i].Total * 100 / largest
		}
	}
	if len(out) > breakdownLimit {
		out = out[:breakdownLimit]
	}
	return out
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return "none"
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
