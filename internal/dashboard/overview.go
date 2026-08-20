package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
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
	// ShowNames asks the page to list the namespaces as chips. It is false
	// when the sentence already named them: saying the same list twice, once
	// as prose and once as chips, is how the panel got twice as tall as the
	// fact it carries.
	ShowNames bool
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
	// SucceededPct, FailedPct and SimulatedPct are each outcome's share of
	// this bar, 0-100. They are heights rather than flex weights because the
	// page's Content-Security-Policy forbids inline styles, so proportions
	// have to arrive as classes.
	SucceededPct int
	FailedPct    int
	SimulatedPct int
}

// ActivityPanel is the last day, one bar per hour.
//
// Bars are divs with a height and a table of the same numbers underneath for
// anybody not reading them visually. A charting library to draw twenty-four
// rectangles would cost a bundler, a release artifact and a request leaving
// the cluster, which this page's own CSP forbids.
type ActivityPanel struct {
	Bars []ActivityBar
	// Busiest is the count the tallest bar represents, which the caption
	// reports as the peak.
	Busiest int
	// Scale is what the top of the chart is worth. It is the busiest hour,
	// or the floor when that is small — and it is what the axis must be
	// labelled with. Labelling the axis with Busiest instead said the top
	// of the chart was 1 while a bar of 1 drew a quarter of the height.
	Scale int
	// Total is every execution in the window.
	Total int
	// Failed is how many of them failed. The tiles above this panel count
	// every record the cluster still holds, which is a different question
	// from "how is it going right now" — and the one the page's own heading
	// asks.
	Failed int
	// Window describes the span in words.
	Window string
}

// Any reports whether anything happened in the window.
func (p ActivityPanel) Any() bool { return p.Total > 0 }

// FailureRate is the share of the window that failed, as a percentage. An
// empty window has no rate rather than a rate of zero, which would read as
// "nothing is failing" when the truth is "nothing has run".
func (p ActivityPanel) FailureRate() int {
	if p.Total == 0 {
		return 0
	}
	return p.Failed * 100 / p.Total
}

// Breakdown is one row of "where is this happening": a name, its counts, and
// the filtered list that shows them.
type Breakdown struct {
	Name      string
	Total     int
	Failed    int
	Simulated int
	// Share is this row's percentage of the largest row, for the inline bar.
	Share int
	// FailedShare is how much of this row's own bar failed, drawn as the
	// tail of it.
	FailedShare int
	URL         string
}

func buildOverview(
	remediations []v1alpha1.Remediation,
	strategies []v1alpha1.RemediationStrategy,
	posture Posture,
	now time.Time,
) OverviewView {
	// One pointer slice, ordered once, shared by every panel below. The
	// records themselves are never copied and the caller's slice is not
	// touched.
	ordered := newestFirst(remediations)

	counts := tally(ordered)

	view := OverviewView{
		Total:         len(ordered),
		StrategyCount: len(strategies),
		PosturePanel:  buildPosturePanel(posture),
		Attention:     buildAttention(ordered),
		Activity:      buildActivity(ordered, now),
		Namespaces:    buildBreakdown(ordered, targetNamespaceOf, byNamespace),
		Strategies:    buildBreakdown(ordered, strategyOf, byStrategy),
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
			Detail: failedDetail(counts.succeeded, counts.failed),
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
			Detail: inFlightDetail(counts),
			Tone:   toneRunning,
			URL:    inFlightURL(counts),
		},
	}

	view.Recent = make([]RemediationRow, 0, min(len(ordered), recentLimit))
	for i, rem := range ordered {
		if i == recentLimit {
			break
		}
		view.Recent = append(view.Recent, buildRow(rem, now, Filter{}, Sort{}))
	}

	if report := buildDryRunReport(ordered, posture.DryRun, now); report != nil {
		view.DryRunning = report
	}
	return view
}

// stateCounts is the headline tally.
//
// awaiting is counted apart from inFlight although it is included in it,
// because the two are in flight for opposite reasons: one is waiting on
// remedik, the other is waiting on a person. A number labelled "pending or
// running" that silently included an approval queue would describe a busy
// operator when the truth is a queue nobody has emptied.
type stateCounts struct {
	succeeded, failed, simulated, inFlight, awaiting int
}

func tally(remediations []*v1alpha1.Remediation) stateCounts {
	var counts stateCounts
	for _, rem := range remediations {
		switch rem.Status.State {
		case v1alpha1.RemediationStateSucceeded:
			counts.succeeded++
		case v1alpha1.RemediationStateFailed:
			counts.failed++
		case v1alpha1.RemediationStateSimulated:
			counts.simulated++
		case v1alpha1.RemediationStateAwaitingApproval:
			counts.awaiting++
			counts.inFlight++
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

// inFlightDetail says what the number is made of, because "in flight" covers
// two situations a reader would act on differently.
func inFlightDetail(counts stateCounts) string {
	working := counts.inFlight - counts.awaiting

	switch {
	case counts.awaiting == 0:
		return "pending or running"
	case working == 0:
		return "waiting for a person"
	default:
		return fmt.Sprintf("%d waiting for a person, %d pending or running",
			counts.awaiting, working)
	}
}

// inFlightURL points at whichever half a reader can do something about.
func inFlightURL(counts stateCounts) string {
	if counts.awaiting > 0 {
		// The queue rather than the filter: these are the records where
		// somebody doing something changes the outcome, and the queue is the
		// page that says how long they have.
		return approvalsPath
	}
	return Filter{State: string(v1alpha1.RemediationStatePending)}.Path()
}

func buildPosturePanel(posture Posture) PosturePanel {
	if posture.Paused {
		return PosturePanel{
			Headline: "Remediation is paused",
			Detail: "Every strategy only reports, whatever its posture says. " +
				"Records still appear, marked Simulated, so nothing about what " +
				"would have happened is lost.",
			Tone: toneWarn,
		}
	}
	panel := PosturePanel{
		Mixed:     posture.Mixed(),
		Live:      posture.Live,
		Reporting: posture.DryRunOnly,
	}

	switch {
	case panel.Mixed && posture.DryRun:
		panel.Headline = "Mixed"
		panel.Detail = fmt.Sprintf(
			"Reporting only, except in %s, where remedik acts.", nameOrCount(posture.Live))
		panel.ShowNames = len(posture.Live) > postureNameLimit
		panel.Tone = toneRunning
	case panel.Mixed:
		panel.Headline = "Mixed"
		panel.Detail = fmt.Sprintf(
			"Acting on matching alerts, except in %s, where remedik only reports.",
			nameOrCount(posture.DryRunOnly))
		panel.ShowNames = len(posture.DryRunOnly) > postureNameLimit
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

func buildAttention(remediations []*v1alpha1.Remediation) AttentionPanel {
	var failed, unheard, untold, interrupted, waiting int

	for _, rem := range remediations {
		if rem.Status.State == v1alpha1.RemediationStateAwaitingApproval {
			waiting++
			continue
		}
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
	failedState := string(v1alpha1.RemediationStateFailed)
	failedURL := Filter{State: failedState}.Path()
	// Each card links to the set it counted, not to every failure. They both
	// pointed at state=Failed, so "64 escalations failed" opened a list of
	// two hundred and twelve — a number the page offered and could not show.
	unheardURL := Filter{State: failedState, Escalation: EscalationFailed}.Path()
	untoldURL := Filter{State: failedState, Escalation: EscalationNone}.Path()

	// First, ahead of anything that already happened: these are the only
	// entries where somebody doing something changes the outcome. Everything
	// below is a report; this is a request.
	//
	// An approval gate that silently accumulates is worse than none — it looks
	// like remediation working — so a queue nobody can see is the failure this
	// entry exists to prevent.
	if waiting > 0 {
		panel.Items = append(panel.Items, AttentionItem{
			Count: waiting,
			Label: plural(waiting, "remediation-remediations") + " waiting for you",
			Detail: "Nothing will run until somebody approves or denies them, and " +
				"they escalate if nobody does.",
			Tone: toneWaiting,
			URL:  approvalsPath,
		})
	}

	// Then, ordered by how much silence each represents.
	if unheard > 0 {
		panel.Items = append(panel.Items, AttentionItem{
			Count: unheard,
			Label: unit(unheard, "escalation-escalations") + " failed",
			Detail: "A remediation failed and the attempt to report it failed too. " +
				"Assume nobody was told.",
			Tone: toneFailed,
			URL:  unheardURL,
		})
	}
	if untold > 0 {
		panel.Items = append(panel.Items, AttentionItem{
			Count: untold,
			Label: unit(untold, "failure-failures") + " with no escalation",
			Detail: "Their strategies declare no onFailure.steps, so no alert went " +
				"anywhere. Not a fault — but worth knowing before concluding it is quiet.",
			Tone: toneWaiting,
			URL:  untoldURL,
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

// ranAt is when a remediation acted, which is what a panel called Activity is
// about. It is within seconds of when the record was made for anything remedik
// created itself, and it is the honest answer for anything that did not arrive
// in real time — a record restored from a backup, or a cluster seeded for a
// demonstration, would otherwise pile a day of work into the minute it was
// written and flatten every other hour to nothing.
//
// A record that has not run has no start, so it counts where it exists: created
// is the only time it has.
func ranAt(rem *v1alpha1.Remediation) time.Time {
	if rem.Status.StartedAt != nil {
		return rem.Status.StartedAt.Time
	}
	return rem.CreationTimestamp.Time
}

func buildActivity(remediations []*v1alpha1.Remediation, now time.Time) ActivityPanel {
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

	for _, rem := range remediations {
		when := ranAt(rem)
		if when.Before(start) {
			continue
		}
		bucket := int(when.Sub(start) / time.Hour)
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
		if rem.Status.State == v1alpha1.RemediationStateFailed {
			panel.Failed++
		}
		if bar.Total > panel.Busiest {
			panel.Busiest = bar.Total
		}
	}

	// Heights are relative to the busiest hour, but not when the busiest hour
	// is tiny. A cluster with one execution an hour rendered twenty-four
	// full-height bars — a wall that reads as a crisis and is a quiet night.
	// Scaling against a floor keeps a quiet day looking quiet; the caption
	// still says what the peak actually was.
	panel.Scale = panel.Busiest
	if panel.Scale < activityScaleFloor {
		panel.Scale = activityScaleFloor
	}

	for i := range panel.Bars {
		bar := &panel.Bars[i]
		if panel.Scale > 0 {
			bar.Percent = bar.Total * 100 / panel.Scale
		}
		if bar.Total > 0 {
			bar.SucceededPct = bar.Succeeded * 100 / bar.Total
			bar.FailedPct = bar.Failed * 100 / bar.Total
			// The last one takes the remainder, so the three always sum to
			// exactly 100 and a stack never leaves a sliver of track showing.
			bar.SimulatedPct = 100 - bar.SucceededPct - bar.FailedPct
		}
	}
	return panel
}

// activityScaleFloor is the smallest peak the chart will scale against.
//
// Four, because a day whose busiest hour saw one remediation should look like
// a quarter of a bar rather than a full one. Above it the scale is the real
// peak and nothing is compressed.
const activityScaleFloor = 4

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
	remediations []*v1alpha1.Remediation,
	key func(*v1alpha1.Remediation) string,
	link func(string) string,
) []Breakdown {
	rows := map[string]*Breakdown{}

	for _, rem := range remediations {
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
			// The share of this row's own bar that failed. Rows near the top
			// are all within a few percent of each other, so a bar encoding
			// volume alone drew five identical tracks; the tail is the part
			// that differs, and it is the part worth seeing first.
			if out[i].Total > 0 {
				out[i].FailedShare = out[i].Failed * 100 / out[i].Total
			}
		}
	}
	if len(out) > breakdownLimit {
		out = out[:breakdownLimit]
	}
	return out
}

// postureNameLimit is how many namespaces the sentence will name before it
// counts them instead.
//
// Three, because "except in staging, canary and dev" reads as a sentence and
// "except in a, b, c, d, e, f, g and h" is a list wearing a sentence's
// clothes. Past the limit the sentence says how many and the chips below say
// which — each doing the job it is good at, and neither repeating the other.
const postureNameLimit = 3

// nameOrCount names a few namespaces, or counts many of them.
func nameOrCount(names []string) string {
	if len(names) > postureNameLimit {
		return fmt.Sprintf("%d namespaces", len(names))
	}
	return joinNames(names)
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
