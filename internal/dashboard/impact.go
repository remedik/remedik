package dashboard

// What the numbers add up to.
//
// The overview counted well and concluded nothing: 1328 / 639 / 214 / 475 are
// inputs to the two questions somebody actually has — is this getting better
// or worse, and how much of it did remedik handle without anybody. Both are
// arithmetic over records the page has already loaded.
//
// Every figure here is defined as a sentence that can be checked against the
// records. Nothing is estimated. In particular there is no "engineer hours
// saved", "MTTR reduced by" or "incidents avoided": each of those needs a
// counterfactual — what a person would have done, and how long they would have
// taken — that remedik cannot observe. A number nobody can derive is worse
// than no number, because it is the one people quote.

import (
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

// paramRange chooses the span the overview describes.
const paramRange = "range"

// medianFloor is the fewest records a median is worth computing over.
//
// Below it the figure is withheld and says so. Two records have a median, and
// it is noise dressed as a measurement.
const medianFloor = 5

// Window is the span the activity and impact panels describe, and the buckets
// the chart draws.
//
// One control governs both panels: a reader who widens the chart to a week is
// asking the whole page about a week, and two ranges on one screen is how a
// page ends up comparing a day with a week and calling it a trend.
type Window struct {
	// Key is the query value, Label the words, Short the chip.
	Key   string
	Label string
	Short string
	// Buckets is how many bars, Bucket how long each one is.
	Buckets int
	Bucket  time.Duration
	// Layout formats a bucket's label.
	Layout string
}

// Span is the whole window.
func (w Window) Span() time.Duration { return time.Duration(w.Buckets) * w.Bucket }

// Start is the beginning of the first bucket, aligned so that bars line up
// with hours or with days rather than with whenever the page was opened.
func (w Window) Start(now time.Time) time.Time {
	return now.Truncate(w.Bucket).Add(-time.Duration(w.Buckets-1) * w.Bucket)
}

// URL is the overview showing this window. The default carries no parameter,
// so the front page stays a bare "/".
func (w Window) URL() string {
	if w.Key == windows[0].Key {
		return "/"
	}
	return "/?" + paramRange + "=" + url.QueryEscape(w.Key)
}

// windows are the spans on offer. The first is the default.
//
// A day is the incident question — is anything wrong now — and a week is the
// review question. Anything longer is a job for the metrics remedik exports,
// not for a page rendered out of records the cluster happens to still hold.
var windows = []Window{
	{
		Key: "24h", Label: "last 24 hours", Short: "24h",
		Buckets: activityHours, Bucket: time.Hour, Layout: "15:04",
	},
	{
		Key: "7d", Label: "last 7 days", Short: "7d",
		Buckets: 7, Bucket: 24 * time.Hour, Layout: "Mon 2",
	},
}

// ParseWindow reads the range. An unknown value is the default rather than an
// error, for the same reason an unknown filter value is honoured: a URL
// pasted into a channel must not become an error page.
func ParseWindow(query url.Values) Window {
	want := query.Get(paramRange)
	for _, window := range windows {
		if window.Key == want {
			return window
		}
	}
	return windows[0]
}

// Windows is every span, with the one in force marked, for the control.
func (w Window) Windows() []WindowChoice {
	choices := make([]WindowChoice, 0, len(windows))
	for _, window := range windows {
		choices = append(choices, WindowChoice{
			Short: window.Short, URL: window.URL(), Selected: window.Key == w.Key,
		})
	}
	return choices
}

// WindowChoice is one range, as the link that selects it.
type WindowChoice struct {
	Short    string
	URL      string
	Selected bool
}

// ImpactPanel is what the window adds up to, and which way it is moving.
type ImpactPanel struct {
	Window  string
	Figures []ImpactFigure
	// Covered names the span actually covered, when retention made it
	// shorter than the one asked for. A seven-day panel over three days of
	// retained records is describing three days, and should say so.
	Covered string
	// Comparable reports there being an earlier window to compare against.
	// Without one, a figure has a value and no direction.
	Comparable bool
}

// Any reports whether anything ran in the window.
func (p ImpactPanel) Any() bool { return len(p.Figures) > 0 }

// ImpactFigure is one number, what it means, and which way it moved.
type ImpactFigure struct {
	Label  string
	Value  string
	Detail string
	// Delta is the movement against the previous window of equal length, in
	// the figure's own units, and Direction says whether that is good news.
	Delta     string
	Direction string
	Tone      string
	URL       string
}

// Directions, as the template names them. "Better" and "worse" rather than
// "up" and "down": a rising failure rate and a rising success rate are the
// same arrow and opposite news.
const (
	directionBetter = "better"
	directionWorse  = "worse"
	directionLevel  = "level"
)

// impactSlice is the arithmetic over one window, computed in a single pass.
type impactSlice struct {
	executed  int
	succeeded int
	// alone is a success nobody had to approve. It is the honest form of what
	// the rest of this industry calls automation coverage: a remediation a
	// person had to agree to was not handled without a person.
	alone int
	// resolutions are alert-to-outcome durations, for the median.
	resolutions []time.Duration
	// oldest is the earliest record seen, so the panel can say what it
	// actually covered after retention has pruned.
	oldest time.Time
}

func buildImpact(
	remediations []*v1alpha1.Remediation, window Window, now time.Time,
) ImpactPanel {
	span := window.Span()
	current := collectImpact(remediations, now.Add(-span), now)
	previous := collectImpact(remediations, now.Add(-2*span), now.Add(-span))

	panel := ImpactPanel{
		Window:     window.Label,
		Comparable: previous.executed > 0,
	}
	if current.executed == 0 {
		return panel
	}
	// Qualified only when the shortfall is worth a sentence. The oldest record
	// in a full window is always a little short of the window itself — that is
	// the current bucket being partial, not history being missing — so the
	// threshold is one bucket rather than any difference at all.
	if !current.oldest.IsZero() {
		if covered := now.Sub(current.oldest); span-covered >= window.Bucket {
			panel.Covered = FormatPeriod(covered)
		}
	}

	panel.Figures = []ImpactFigure{
		handledAlone(current, previous, panel.Comparable),
		alertToOutcome(current, previous, panel.Comparable),
		volume(current, previous, panel.Comparable),
	}
	return panel
}

// collectImpact is one pass over the records for one window.
func collectImpact(
	remediations []*v1alpha1.Remediation, from, to time.Time,
) impactSlice {
	var slice impactSlice

	for _, rem := range remediations {
		when := ranAt(rem)
		if when.Before(from) || !when.Before(to) {
			continue
		}
		// Simulated records are excluded throughout: nothing happened, so
		// counting them as handled would make dry-run look like success.
		switch rem.Status.State {
		case v1alpha1.RemediationStateSucceeded:
			slice.executed++
			slice.succeeded++
			if rem.Spec.Approval == nil {
				slice.alone++
			}
		case v1alpha1.RemediationStateFailed:
			slice.executed++
		default:
			continue
		}

		if slice.oldest.IsZero() || when.Before(slice.oldest) {
			slice.oldest = when
		}
		if resolved, ok := resolution(rem); ok {
			slice.resolutions = append(slice.resolutions, resolved)
		}
	}
	return slice
}

// resolution is how long the alert was true before remedik was done with it.
//
// From the alert firing rather than from the record being created, because
// the gap between those two is Alertmanager's group_wait and the gateway's
// own latency — which is part of the answer to "how long was this broken".
func resolution(rem *v1alpha1.Remediation) (time.Duration, bool) {
	firing, done := rem.Spec.Alert.StartsAt, rem.Status.CompletedAt
	if firing == nil || done == nil || firing.IsZero() || done.IsZero() {
		return 0, false
	}
	if span := done.Sub(firing.Time); span > 0 {
		return span, true
	}
	return 0, false
}

func handledAlone(current, previous impactSlice, comparable bool) ImpactFigure {
	share := percent(current.alone, current.executed)
	figure := ImpactFigure{
		Label: "Handled without a person",
		Value: fmt.Sprintf("%d%%", share),
		Detail: fmt.Sprintf("%d of %s ran for real, succeeded, and needed no approval",
			current.alone, plural(current.executed, "execution")),
		Tone: toneOK,
		URL:  Filter{State: string(v1alpha1.RemediationStateSucceeded)}.Path(),
	}
	if !comparable {
		return figure
	}

	// Points, not percent: a rise from 50% to 60% is ten points, and calling
	// it "20% better" is the oldest way to mislead with a true number.
	was := percent(previous.alone, previous.executed)
	figure.Delta, figure.Direction = points(share - was)
	return figure
}

func alertToOutcome(current, previous impactSlice, comparable bool) ImpactFigure {
	figure := ImpactFigure{
		Label: "Alert to outcome",
		Tone:  toneMuted,
		URL:   remediationsPath,
	}

	if len(current.resolutions) < medianFloor {
		figure.Value = "—"
		figure.Detail = fmt.Sprintf(
			"%s in this window carry both a firing time and an outcome — too few for a median",
			plural(len(current.resolutions), "record"))
		return figure
	}

	median := medianOf(current.resolutions)
	figure.Value = FormatDuration(median)
	figure.Detail = fmt.Sprintf("median over %s, from the alert firing",
		plural(len(current.resolutions), "record"))

	if !comparable || len(previous.resolutions) < medianFloor {
		return figure
	}
	was := medianOf(previous.resolutions)
	switch change := median - was; {
	case change > 0:
		figure.Delta, figure.Direction = FormatDuration(change)+" slower", directionWorse
	case change < 0:
		figure.Delta, figure.Direction = FormatDuration(-change)+" faster", directionBetter
	default:
		figure.Delta, figure.Direction = "unchanged", directionLevel
	}
	return figure
}

func volume(current, previous impactSlice, comparable bool) ImpactFigure {
	figure := ImpactFigure{
		Label:  "Ran for real",
		Value:  fmt.Sprint(current.executed),
		Detail: fmt.Sprintf("%s failed", plural(current.executed-current.succeeded, "execution")),
		Tone:   toneMuted,
		URL:    remediationsPath,
	}
	if !comparable {
		return figure
	}

	// More remediation is not better or worse on its own — it is more alerts —
	// so this one moves without a verdict attached.
	switch change := current.executed - previous.executed; {
	case change > 0:
		figure.Delta, figure.Direction = fmt.Sprintf("%d more than the window before", change), directionLevel
	case change < 0:
		figure.Delta, figure.Direction = fmt.Sprintf("%d fewer than the window before", -change), directionLevel
	default:
		figure.Delta, figure.Direction = "the same as the window before", directionLevel
	}
	return figure
}

// points renders a change in percentage points, with its verdict.
func points(change int) (string, string) {
	switch {
	case change > 0:
		return fmt.Sprintf("up %d points", change), directionBetter
	case change < 0:
		return fmt.Sprintf("down %d points", -change), directionWorse
	default:
		return "level", directionLevel
	}
}

// medianOf is the middle value, or the lower of the two middles.
//
// Median rather than mean, because one interrupted record that sat for a day
// would otherwise move a figure describing ninety seconds. It sorts a copy:
// the caller's slice is the window's own and is read again.
func medianOf(durations []time.Duration) time.Duration {
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(len(sorted)-1)/2]
}
