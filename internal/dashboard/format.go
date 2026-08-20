package dashboard

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Times are formatted in UTC throughout.
//
// The alternative is the operator pod's local zone, which is whatever the
// image happens to carry and is not the reader's zone either. One explicit
// zone, stated on every timestamp, is the version nobody has to guess at
// during an incident.

// FormatAge renders how long ago t was, in the short form kubectl uses.
func FormatAge(t time.Time, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 0:
		// Clock skew between the API server and the operator; reporting a
		// negative age would be more confusing than rounding it away.
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

// FormatAgeOf is FormatAge for an optional Kubernetes timestamp.
func FormatAgeOf(t *metav1.Time, now time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return FormatAge(t.Time, now)
}

// FormatTimestamp renders an absolute time, for the tooltip behind a
// relative one and anywhere the exact moment is the point.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05 MST")
}

// FormatTimestampOf is FormatTimestamp for an optional Kubernetes timestamp.
func FormatTimestampOf(t *metav1.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return FormatTimestamp(t.Time)
}

// FormatClock renders the time of day, for "last updated".
func FormatClock(t time.Time) string {
	return t.UTC().Format("15:04:05 MST")
}

// FormatDuration renders how long something took, at a precision that
// matches its length: a step that took 820ms and a run that took four
// minutes are both worth reading exactly, and neither is worth reading to
// the nanosecond.
func FormatDuration(d time.Duration) string {
	switch {
	case d < 0:
		return ""
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// FormatSpan renders the duration between two optional timestamps. An
// execution still running has no end, and gets no span rather than a
// misleading one.
//
// A span of zero gets none either. Timestamps here have second granularity,
// so a step that failed before it did anything starts and ends at the same
// instant — and "0ms" reads as a measurement of something that never ran.
// The pages already render a missing duration as an em dash, which is the
// honest answer.
func FormatSpan(from, to *metav1.Time) string {
	if from == nil || to == nil || from.IsZero() || to.IsZero() {
		return ""
	}
	span := to.Sub(from.Time)
	if span <= 0 {
		return ""
	}
	return FormatDuration(span)
}

// FormatCountdown renders time remaining, at the precision somebody deciding
// under it needs.
//
// Seconds while there are only seconds left, because that is when the number
// changes what a reader does; and a padded second past a minute, so a queue of
// countdowns does not jitter sideways as each one crosses ten.
func FormatCountdown(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// FormatPeriod renders a length of time in words, for the sentence that
// says how long a dry-run trial has been running.
func FormatPeriod(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Hours()/24), "day")
	}
}
