package dashboard

// Formatting: states to words, tones to palette names, numbers to prose.
//
// Separate from format.go, which converts times. These are the ones that
// decide what a page says rather than how a timestamp reads, and they are
// shared by every page — which is why they were the largest thing left in
// view.go and the least related to anything else in it.

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

// displayState names the state a record is in. An empty state is a record
// the reconciler has not reached yet, which reads as Pending.
func displayState(state v1alpha1.RemediationState) string {
	if state == "" {
		return string(v1alpha1.RemediationStatePending)
	}
	return string(state)
}

func stateTone(state v1alpha1.RemediationState) string {
	switch state {
	case v1alpha1.RemediationStateSucceeded:
		return toneOK
	case v1alpha1.RemediationStateFailed:
		return toneFailed
	case v1alpha1.RemediationStateSimulated:
		return toneDryRun
	case v1alpha1.RemediationStateRunning:
		return toneRunning
	case v1alpha1.RemediationStatePending:
		return toneWaiting
	case v1alpha1.RemediationStateAwaitingApproval:
		// Warn, not waiting: a record waiting for a retry needs nobody, and one
		// waiting for a person needs somebody now. They must not look alike.
		return toneWarn
	default:
		return toneWaiting
	}
}

func phaseTone(phase v1alpha1.StepPhase) string {
	switch phase {
	case v1alpha1.StepPhaseSucceeded:
		return toneOK
	case v1alpha1.StepPhaseFailed:
		return toneFailed
	case v1alpha1.StepPhaseSimulated:
		return toneDryRun
	case v1alpha1.StepPhaseRunning:
		return toneRunning
	case v1alpha1.StepPhaseSkipped:
		return toneMuted
	case v1alpha1.StepPhasePending:
		return toneWaiting
	default:
		return toneWaiting
	}
}

// notReadyMessage returns the message of a Ready condition that is false.
// A strategy that cannot run is worth saying so on the page that lists it.
func notReadyMessage(conditions []metav1.Condition) string {
	for i := range conditions {
		if conditions[i].Type != "Ready" || conditions[i].Status != metav1.ConditionFalse {
			continue
		}
		if msg := conditions[i].Message; msg != "" {
			return msg
		}
		return conditions[i].Reason
	}
	return ""
}

// firstPlan is the plan line of a record's first recorded step — the
// sentence that says what would have been done.
func firstPlan(rem *v1alpha1.Remediation) string {
	for i := range rem.Status.Steps {
		if plan := rem.Status.Steps[i].Plan; plan != "" {
			return plan
		}
	}
	return ""
}

func successRate(succeeded, failed int) string {
	total := succeeded + failed
	if total == 0 {
		return "nothing executed yet"
	}
	return fmt.Sprintf("%d%% of executed runs", percent(succeeded, total))
}

func failedDetail(failed int) string {
	if failed == 0 {
		return "none"
	}
	return "needs a look"
}

func percent(part, total int) int {
	if total == 0 {
		return 0
	}
	return int(float64(part)/float64(total)*100 + 0.5)
}

// shortDuration renders a guard's duration the way it was written in the
// manifest — "15m", not "15m0s".
func shortDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	default:
		return d.String()
	}
}

// unit is plural's other half: the noun alone, for places that already show
// the number. "1 1 escalation failed" is what happens without it.
func unit(n int, name string) string {
	singular, pluralForm, irregular := strings.Cut(name, "-")
	if !irregular {
		pluralForm = singular + "s"
	}
	if n == 1 {
		return singular
	}
	return pluralForm
}

// plural handles the units this package counts. Irregular plurals are given
// explicitly as "singular-plural"; everything else takes an s.
func plural(n int, unit string) string {
	singular, pluralForm, irregular := strings.Cut(unit, "-")
	if !irregular {
		pluralForm = singular + "s"
	}
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, pluralForm)
}
