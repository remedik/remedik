package dashboard

// The remediation detail page: what one execution did, step by step.
//
// It is the page an incident ends on, so it carries the most view types of
// any of them — the steps joined to their plan, the escalation kept separate
// from the remediation's own steps, and the alert that started it.

import (
	"fmt"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

// RemediationView is one execution, in full.
type RemediationView struct {
	Page

	Name     string
	Strategy string
	Target   string
	State    string
	Tone     string
	Summary  string
	Reason   string
	Message  string
	DryRun   bool
	Attempt  int32
	// MaxAttempts states the retry budget the way it reads on the page:
	// one attempt, plus the retries the strategy allowed.
	MaxAttempts int32
	Created     string
	CreatedAge  string
	Started     string
	Completed   string
	Duration    string
	Alert       AlertView
	Steps       []StepView
	// Escalation is who was told, and whether telling them worked. Nil when
	// the strategy declares no escalation — which is itself worth seeing on
	// a failed remediation, so the page says so rather than staying silent.
	Escalation *EscalationView
	// Failed is the terminal state, kept as a bool because the page asks the
	// question more than once and State is a display string.
	Failed bool
}

// EscalationView is the onFailure plan and what became of it.
//
// It is deliberately a separate block on the page. A page that folded these
// into the steps would make "we told PagerDuty" read as a fourth attempt at
// the restart, and would hide the case that matters most: the remediation
// failed and the page failed too, so nobody knows.
type EscalationView struct {
	Phase     string
	Tone      string
	Message   string
	Completed string
	Steps     []StepView
	// Sent reports whether anybody was actually told.
	Sent bool
}

// ShowMessage reports whether the escalation's own message adds anything to
// the steps below it. With one step it is the same sentence twice, and a page
// that repeats itself looks like it is padding.
func (v EscalationView) ShowMessage() bool {
	if v.Message == "" {
		return false
	}
	for _, step := range v.Steps {
		if step.Message == v.Message {
			return false
		}
	}
	return true
}

// NobodyWasTold reports a failed remediation with no escalation declared.
//
// It is not a criticism — most strategies do not need one. It is on the page
// because this is the moment somebody discovers the feature exists, and
// because "it failed and no alert went anywhere" is a fact worth stating out
// loud rather than leaving to be inferred from an absence.
func (v RemediationView) NobodyWasTold() bool { return v.Failed && v.Escalation == nil }

// ShowRawMessage reports whether the status message adds anything to the
// summary. For a failed step the summary already quotes it, and saying the
// same thing twice makes a page look like it is padding.
func (v RemediationView) ShowRawMessage() bool {
	if v.Message == "" {
		return false
	}
	return v.Reason != v1alpha1.ReasonStepFailed && v.Reason != v1alpha1.ReasonUnknownAction
}

// AlertView is the alert that triggered an execution.
type AlertView struct {
	Name        string
	Fingerprint string
	StartsAt    string
	StartsAge   string
	Labels      []Label
}

// StepView is one step of the plan, joined with whatever happened to it.
type StepView struct {
	Number   int
	Action   string
	Target   string
	Phase    string
	Tone     string
	Plan     string
	Message  string
	Params   []Label
	Started  string
	Duration string
	// Kubectl is the equivalent command a human would have typed. Shown so
	// that the change is reviewable by someone who has never read remedik's
	// source — which is most of the people who will read this page.
	Kubectl string
	// Outputs are what the action specifically knew: replicas, an exit
	// code, a revision.
	Outputs []Label
	// Verified is what the action's own post-condition check found. Empty
	// means the action does not check its work, or this was a dry run.
	Verified string
	// Ran reports whether this step has a recorded outcome. A step with
	// none never started, which is a different thing from one that was
	// skipped after an earlier failure.
	Ran bool
}

// StrategiesView is the strategy list.

func buildRow(rem *v1alpha1.Remediation, now time.Time) RemediationRow {
	state := displayState(rem.Status.State)
	return RemediationRow{
		Name:      rem.Name,
		URL:       "/remediations/" + rem.Name,
		Strategy:  rem.Spec.StrategyName,
		Target:    rem.Spec.Target,
		Alert:     rem.Spec.Alert.Name,
		State:     state,
		Tone:      stateTone(rem.Status.State),
		Age:       FormatAge(rem.CreationTimestamp.Time, now),
		AgeExact:  FormatTimestamp(rem.CreationTimestamp.Time),
		Duration:  FormatSpan(rem.Status.StartedAt, rem.Status.CompletedAt),
		Attempt:   rem.Status.Attempt,
		DryRun:    rem.Spec.DryRun,
		Reason:    rem.Status.Reason,
		Escalated: escalationMarker(rem.Status.Escalation),
	}
}

func escalationMarker(esc *v1alpha1.EscalationStatus) string {
	switch {
	case esc == nil:
		return ""
	case esc.Phase == v1alpha1.StepPhaseSucceeded:
		return escalationSent
	default:
		return escalationFailed
	}
}

func buildRemediation(rem *v1alpha1.Remediation, now time.Time) RemediationView {
	view := RemediationView{
		Name:        rem.Name,
		Strategy:    rem.Spec.StrategyName,
		Target:      rem.Spec.Target,
		State:       displayState(rem.Status.State),
		Tone:        stateTone(rem.Status.State),
		Reason:      rem.Status.Reason,
		Message:     rem.Status.Message,
		DryRun:      rem.Spec.DryRun,
		Attempt:     rem.Status.Attempt,
		MaxAttempts: rem.Spec.Retries + 1,
		Created:     FormatTimestamp(rem.CreationTimestamp.Time),
		CreatedAge:  FormatAge(rem.CreationTimestamp.Time, now),
		Started:     FormatTimestampOf(rem.Status.StartedAt),
		Completed:   FormatTimestampOf(rem.Status.CompletedAt),
		Duration:    FormatSpan(rem.Status.StartedAt, rem.Status.CompletedAt),
		Alert: AlertView{
			Name:        rem.Spec.Alert.Name,
			Fingerprint: rem.Spec.Alert.Fingerprint,
			StartsAt:    FormatTimestampOf(rem.Spec.Alert.StartsAt),
			StartsAge:   FormatAgeOf(rem.Spec.Alert.StartsAt, now),
			Labels:      sortedLabels(rem.Spec.Alert.Labels),
		},
		Steps: buildSteps(rem),
	}
	view.Failed = rem.Status.State == v1alpha1.RemediationStateFailed
	view.Escalation = buildEscalation(rem)
	view.Summary = summarise(rem, view.Steps)
	return view
}

// applyFilter keeps the records the filter admits, without copying when
// nothing is being narrowed.
// applyFilter keeps the records a filter matches.
//
// It works on pointers, which is not a micro-optimisation: the previous
// version allocated capacity for every record and copied each 552-byte struct
// it kept, so filtering ten thousand records down to sixty allocated five and
// a half megabytes — more than not filtering at all, which is the wrong way
// round for an operation that does less work.

// buildSteps joins the plan with what happened to it.
//
// The plan is on the spec and the outcome is on the status, and they can
// disagree in length: a run interrupted after two of three steps has three
// planned and two recorded. Joining by index rather than zipping means a
// step that never started still appears, which is exactly what someone
// reading a failure needs to see.
func buildSteps(rem *v1alpha1.Remediation) []StepView {
	return joinSteps(rem.Spec.Steps, rem.Status.Steps)
}

// joinSteps pairs a plan with its outcome. It serves the remediation's own
// steps and the escalation's alike, because they are the same join.
func joinSteps(plan []v1alpha1.Step, recorded []v1alpha1.StepStatus) []StepView {
	status := make(map[int32]*v1alpha1.StepStatus, len(recorded))
	highest := -1
	for i := range recorded {
		st := &recorded[i]
		status[st.Index] = st
		if int(st.Index) > highest {
			highest = int(st.Index)
		}
	}

	count := max(len(plan), highest+1)
	steps := make([]StepView, 0, count)

	for i := range count {
		view := StepView{Number: i + 1, Phase: string(v1alpha1.StepPhasePending)}

		if i < len(plan) {
			view.Action = plan[i].Action
			view.Params = sortedLabels(plan[i].With)
		}

		if st, ok := status[int32(i)]; ok {
			view.Ran = true
			if st.Action != "" {
				view.Action = st.Action
			}
			view.Target = st.Target
			view.Phase = string(st.Phase)
			view.Plan = st.Plan
			view.Message = st.Message
			view.Kubectl = st.Kubectl
			view.Outputs = sortedLabels(st.Outputs)
			view.Verified = st.Verified
			view.Started = FormatTimestampOf(st.StartedAt)
			view.Duration = FormatSpan(st.StartedAt, st.CompletedAt)
		}

		view.Tone = phaseTone(v1alpha1.StepPhase(view.Phase))
		steps = append(steps, view)
	}

	return steps
}

// buildEscalation renders the onFailure plan's outcome, when there was one.
func buildEscalation(rem *v1alpha1.Remediation) *EscalationView {
	esc := rem.Status.Escalation
	if esc == nil {
		return nil
	}

	sent := esc.Phase == v1alpha1.StepPhaseSucceeded
	return &EscalationView{
		Phase:     string(esc.Phase),
		Tone:      phaseTone(esc.Phase),
		Message:   esc.Message,
		Completed: FormatTimestampOf(esc.CompletedAt),
		Steps:     joinSteps(rem.Spec.EscalationSteps, esc.Steps),
		Sent:      sent,
	}
}

// summarise writes the one line that answers "so what happened?" without
// making the reader assemble it from the fields below.
func summarise(rem *v1alpha1.Remediation, steps []StepView) string {
	switch rem.Status.State {
	case v1alpha1.RemediationStateSucceeded:
		return fmt.Sprintf("Completed %s.", plural(len(steps), "step"))

	case v1alpha1.RemediationStateSimulated:
		return "Dry-run: the plan below was recorded and nothing in the cluster was changed."

	case v1alpha1.RemediationStateFailed:
		switch rem.Status.Reason {
		case v1alpha1.ReasonInterrupted:
			return "The operator restarted while this attempt was running. It was failed rather " +
				"than resumed, because silently repeating a step that had already changed " +
				"something is the worse outcome."
		case v1alpha1.ReasonUnknownAction:
			return "A step named an action this build does not implement. " + rem.Status.Message
		case v1alpha1.ReasonStepFailed:
			if step, ok := failedStep(steps); ok {
				return fmt.Sprintf("Step %d (%s) failed: %s", step.Number, step.Action, step.Message)
			}
			return "A step failed and no retries remained. " + rem.Status.Message
		default:
			if rem.Status.Message != "" {
				return rem.Status.Message
			}
			return "The execution failed."
		}

	case v1alpha1.RemediationStateRunning:
		return fmt.Sprintf("Attempt %d is running.", rem.Status.Attempt)

	case v1alpha1.RemediationStatePending:
		if rem.Status.Attempt > 0 {
			return fmt.Sprintf("Attempt %d failed; waiting to retry (%s allowed).",
				rem.Status.Attempt, plural(int(rem.Spec.Retries), "retry-retries"))
		}
		return "Created and waiting for the reconciler to pick it up."

	default:
		return "Created and waiting for the reconciler to pick it up."
	}
}

func failedStep(steps []StepView) (StepView, bool) {
	for _, step := range steps {
		if step.Phase == string(v1alpha1.StepPhaseFailed) {
			return step, true
		}
	}
	return StepView{}, false
}
