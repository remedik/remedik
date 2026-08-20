package dashboard

// The shape of one remediation in time.
//
// The detail page had the alert, the wait for a person, the attempts and the
// escalation as four sections with timestamps in them, and no reader assembles
// four sections into an order. This is that order, once, with the time elapsed
// between each pair of moments.
//
// It is deliberately not a Gantt chart. Kubernetes timestamps have second
// granularity and most remediations complete inside one second, so drawing
// those durations to scale would produce one rectangle and imply a precision
// the data does not carry. A bar is drawn only where there is a second to
// draw; where there is not, the page says the whole thing took under a second,
// which is itself the answer to "was it slow".

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

// barFloor is the shortest span worth drawing to scale.
const barFloor = time.Second

// historyLimit is how many earlier records for the same target the page
// shows before sending the reader to the filtered list.
const historyLimit = 6

// Timeline is the record as one ordered sequence.
type Timeline struct {
	Entries []TimelineEntry
	// Total is the whole span in words, and Instant reports that it fell
	// inside a single second.
	Total   string
	Instant bool
}

// Any reports whether there is anything to draw.
func (t Timeline) Any() bool { return len(t.Entries) > 0 }

// TimelineEntry is one moment, and what happened at it.
type TimelineEntry struct {
	Label  string
	Detail string
	Tone   string
	// At is the absolute time, empty for a moment nothing recorded a time
	// for — an approval decision, which the API records as a decision and
	// not as a timestamp.
	At string
	// Elapsed is the gap since the previous entry, empty on the first.
	Elapsed string
	// Duration is this entry's own length, and Bar its share of the whole
	// span, 0 when there is nothing worth drawing.
	Duration string
	Bar      int
}

func buildTimeline(rem *v1alpha1.Remediation) Timeline {
	var line Timeline

	// Collected with their times, so elapsed gaps and bar widths are
	// arithmetic rather than string comparison.
	type moment struct {
		entry TimelineEntry
		at    time.Time
		known bool
		// span is the moment's own duration, for the bar.
		span time.Duration
	}
	var moments []moment

	add := func(label, detail, tone string, at *metav1.Time, span time.Duration) {
		m := moment{
			entry: TimelineEntry{Label: label, Detail: detail, Tone: tone},
			span:  span,
		}
		if at != nil && !at.IsZero() {
			m.at, m.known = at.Time, true
			m.entry.At = FormatTimestamp(at.Time)
		}
		if span >= barFloor {
			m.entry.Duration = FormatDuration(span)
		}
		moments = append(moments, m)
	}

	if firing := rem.Spec.Alert.StartsAt; firing != nil && !firing.IsZero() {
		add("Alert fired", rem.Spec.Alert.Name, toneWarn, firing, 0)
	}
	add("Recorded", fmt.Sprintf("%s matched, %s",
		rem.Spec.StrategyName, postureWord(rem.Spec.DryRun)),
		toneMuted, &rem.CreationTimestamp, 0)

	// A person, when there was one. The API records the decision and who made
	// it, and no time for it — so this entry carries no timestamp rather than
	// borrowing one from the moment either side of it.
	if decision := rem.Spec.Approval; decision != nil {
		add(approvalWord(decision.Decision), approvalDetail(decision), toneWaiting, nil, 0)
	} else if rem.Status.State == v1alpha1.RemediationStateAwaitingApproval {
		add("Waiting for a person", "nothing is resolved or planned until it is approved",
			toneWarn, nil, 0)
	}

	if started := rem.Status.StartedAt; started != nil && !started.IsZero() {
		add(attemptWord(rem.Status.Attempt), "", toneRunning, started, 0)
	}

	for i := range rem.Status.Steps {
		step := &rem.Status.Steps[i]
		add(fmt.Sprintf("Step %d — %s", step.Index+1, step.Action),
			stepDetail(step), phaseTone(step.Phase),
			step.StartedAt, between(step.StartedAt, step.CompletedAt))
	}

	if done := rem.Status.CompletedAt; done != nil && !done.IsZero() {
		add(displayState(rem.Status.State), rem.Status.Reason,
			stateTone(rem.Status.State), done, 0)
	}

	if esc := rem.Status.Escalation; esc != nil {
		add(escalationWord(esc), escalationDetail(esc), phaseTone(esc.Phase),
			esc.CompletedAt, 0)
	}

	if len(moments) < 2 {
		return line
	}

	// The span the bars are drawn against, and the gaps between moments.
	var first, last time.Time
	for _, m := range moments {
		if !m.known {
			continue
		}
		if first.IsZero() || m.at.Before(first) {
			first = m.at
		}
		if m.at.After(last) {
			last = m.at
		}
	}
	total := last.Sub(first)
	line.Instant = total < barFloor
	line.Total = FormatDuration(total)
	if line.Instant {
		line.Total = ""
	}

	var previous time.Time
	var seen bool
	line.Entries = make([]TimelineEntry, 0, len(moments))
	for _, m := range moments {
		entry := m.entry
		if m.known {
			if seen {
				if gap := m.at.Sub(previous); gap >= barFloor {
					entry.Elapsed = "+" + FormatDuration(gap)
				}
			}
			previous, seen = m.at, true
		}
		if m.span >= barFloor && total > 0 {
			entry.Bar = int(m.span * 100 / total)
		}
		line.Entries = append(line.Entries, entry)
	}
	return line
}

// between is the length of a step, when both ends were recorded.
func between(from, to *metav1.Time) time.Duration {
	if from == nil || to == nil || from.IsZero() || to.IsZero() {
		return 0
	}
	if span := to.Sub(from.Time); span > 0 {
		return span
	}
	return 0
}

func postureWord(dryRun bool) string {
	if dryRun {
		return "reporting only"
	}
	return "acting"
}

func attemptWord(attempt int32) string {
	if attempt > 1 {
		return fmt.Sprintf("Attempt %d started", attempt)
	}
	return "Started"
}

func approvalWord(decision v1alpha1.ApprovalDecision) string {
	if decision == v1alpha1.ApprovalDeny {
		return "Denied by a person"
	}
	return "Approved by a person"
}

func approvalDetail(decision *v1alpha1.Approval) string {
	who := decision.By
	if who == "" {
		who = "nobody named"
	}
	if decision.Note != "" {
		return who + " — " + decision.Note
	}
	return who
}

func stepDetail(step *v1alpha1.StepStatus) string {
	switch {
	case step.Message != "":
		return step.Message
	case step.Plan != "":
		return step.Plan
	default:
		return string(step.Phase)
	}
}

func escalationWord(esc *v1alpha1.EscalationStatus) string {
	if esc.Phase == v1alpha1.StepPhaseSucceeded {
		return "Somebody was told"
	}
	return "Telling somebody failed"
}

func escalationDetail(esc *v1alpha1.EscalationStatus) string {
	if esc.Message != "" {
		return esc.Message
	}
	if esc.Phase == v1alpha1.StepPhaseSucceeded {
		return "the escalation went out"
	}
	return "assume nobody knows"
}

// --------------------------------------------------------------------------
// This target, before
// --------------------------------------------------------------------------

// TargetHistory is what else has happened to the object being read about.
//
// A remediation that keeps being needed is a different problem from one that
// failed, and it is invisible from the page of any single record. It reuses
// the target filter: the panel is a summary of a page that already exists.
type TargetHistory struct {
	Target string
	// URL is the whole history, filtered to this target.
	URL string
	// Marks are the most recent records, newest first, including the one
	// being read.
	Marks []TargetMark
	// More is how many are not shown.
	More int
	// Repeated reports the same target having been remediated more than once,
	// which is the observation worth making out loud.
	Repeated bool
}

// TargetMark is one earlier outcome.
type TargetMark struct {
	Name string
	URL  string
	// State and Tone are the outcome, Age when it happened.
	State string
	Tone  string
	Age   string
	// Current marks the record being read, so the page shows where it sits
	// in its own history.
	Current bool
}

func buildTargetHistory(
	rem *v1alpha1.Remediation, all []v1alpha1.Remediation, now time.Time,
) *TargetHistory {
	if rem.Spec.Target == "" {
		return nil
	}

	history := &TargetHistory{
		Target: rem.Spec.Target,
		URL:    Filter{Target: rem.Spec.Target}.Path(),
	}

	// Newest first, over the records for this target only. The whole list is
	// already in memory; this is one pass and a sort of what it kept.
	var mine []*v1alpha1.Remediation
	for i := range all {
		if all[i].Spec.Target == rem.Spec.Target {
			mine = append(mine, &all[i])
		}
	}
	if len(mine) < 2 {
		return nil
	}
	sortNewestFirst(mine)

	history.Repeated = true
	for _, other := range mine {
		if len(history.Marks) == historyLimit {
			history.More = len(mine) - historyLimit
			break
		}
		history.Marks = append(history.Marks, TargetMark{
			Name:    other.Name,
			URL:     "/remediations/" + other.Name,
			State:   displayState(other.Status.State),
			Tone:    stateTone(other.Status.State),
			Age:     FormatAge(other.CreationTimestamp.Time, now),
			Current: other.Name == rem.Name,
		})
	}
	return history
}
