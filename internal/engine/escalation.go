package engine

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

// Labels remedik sets on an escalation's context. They are prefixed so they
// cannot be confused with an alert's own labels, and they overwrite any alert
// label of the same name: an escalation that can be lied to by whoever writes
// the alerting rules is worse than no escalation.
const (
	LabelEscalationRemediation = "remedik_remediation"
	LabelEscalationStrategy    = "remedik_strategy"
	LabelEscalationTarget      = "remedik_target"
	LabelEscalationReason      = "remedik_reason"
	LabelEscalationMessage     = "remedik_message"
	LabelEscalationAttempts    = "remedik_attempts"
	LabelEscalationDryRun      = "remedik_dry_run"
)

// EscalationTimeout bounds the whole onFailure plan.
//
// The remediation has already failed and the incident is live, so an
// escalation that hangs holds the reconcile worker open while nobody is being
// told anything. Better to give up and record that the page did not go out.
const EscalationTimeout = 2 * time.Minute

// escalate runs the strategy's onFailure plan and records what happened.
//
// It never returns an error to the caller. The remediation has already failed;
// there is no worse state to move to, and a reconcile error here would retry
// the whole attempt and re-page. What went wrong is written into the status,
// which is where somebody will look.
func (r *RemediationReconciler) escalate(
	ctx context.Context,
	rem *v1alpha1.Remediation,
	attempts int32,
	reason, message string,
	log *slog.Logger,
) *v1alpha1.EscalationStatus {
	if len(rem.Spec.EscalationSteps) == 0 {
		return nil
	}

	// A fresh deadline, not the reconcile's: the escalation is the last
	// thing that happens and must not inherit whatever budget the failed
	// attempt left behind.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), EscalationTimeout)
	defer cancel()

	dryRun := rem.Spec.DryRun
	log = log.With("escalation_steps", len(rem.Spec.EscalationSteps))
	log.Info("remediation failed for good; escalating")

	runner := &StepRunner{
		Registry:    r.Registry,
		Remediation: rem.Name,
		Strategy:    rem.Spec.StrategyName,
		Namespace:   rem.Namespace,
		// Never a dry run, whatever the remediation was. The steps are told
		// the run was simulated instead, through the labels below, so an
		// escalation can say so rather than being silently skipped — which
		// would leave the one path an operator most wants to trial as the
		// one path a trial never exercises.
		DryRun: false,
		Events: r.stepEvents(rem),
		Now:    r.Now,
	}

	labels := escalationLabels(rem, attempts, reason, message, dryRun)
	steps, delivered, lastErr := runChannels(ctx, runner, labels,
		rem.Spec.EscalationSteps, rem.Spec.EscalationMode)

	completed := metav1.NewTime(r.now())
	status := &v1alpha1.EscalationStatus{
		Phase:       v1alpha1.StepPhaseSucceeded,
		Steps:       steps,
		CompletedAt: &completed,
	}
	switch {
	case !delivered:
		status.Phase = v1alpha1.StepPhaseFailed
		status.Message = lastErr.Error()
		log.Error("escalation failed on every channel: nobody was told", "err", lastErr)
	case lastErr != nil:
		// Somebody was told, and a channel is broken. Both facts matter, and
		// only one of them is an emergency.
		log.Warn("escalation delivered, but a channel failed", "err", lastErr)
	default:
		log.Info("escalation sent")
	}

	r.metrics().EscalationFinished(rem.Spec.StrategyName, string(status.Phase))
	return status
}

// runChannels runs the escalation's steps.
//
// It deliberately does not use StepRunner.Run, which stops at the first
// failure. That rule is right for a remediation plan — step two of "scale up,
// then restart" must not act on a scale that did not happen — and inverted
// here, where the steps are alternative ways to reach a person. A fallback
// that stops at the first failure is a single point of failure, and an
// invisible one: every channel succeeds when the path is tested.
//
// The ten lines of duplication are the point. Somebody reading this file sees
// that every channel is tried without having to hold the runner's semantics
// in their head, and the two rules cannot drift into each other.
func runChannels(
	ctx context.Context,
	runner *StepRunner,
	labels map[string]string,
	plan []v1alpha1.Step,
	mode v1alpha1.EscalationMode,
) (steps []v1alpha1.StepStatus, delivered bool, lastErr error) {
	steps = make([]v1alpha1.StepStatus, 0, len(plan))

	for i, step := range plan {
		// firstSuccess is an ordered fallback: once a channel has got
		// through, calling the rest would page the same person twice, which
		// is how people come to remove their fallback.
		if delivered && mode == v1alpha1.EscalationModeFirstSuccess {
			steps = append(steps, v1alpha1.StepStatus{
				Index:   int32(i), //nolint:gosec // bounded by the CRD at 8
				Action:  step.Action,
				Phase:   v1alpha1.StepPhaseSkipped,
				Message: "an earlier channel already reached somebody",
			})
			continue
		}

		status, err := runner.Step(ctx, i, labels, step)
		steps = append(steps, status)
		if err != nil {
			lastErr = err
			continue
		}
		delivered = true
	}

	return steps, delivered, lastErr
}

// escalationLabels describes the failed remediation to the escalation steps.
//
// It is the whole interface: "webhook.call" sends these as the JSON body's
// labels and "job.run" turns them into REMEDIK_ALERT_* environment variables,
// so an escalation needs no templating to say what happened.
func escalationLabels(
	rem *v1alpha1.Remediation, attempts int32, reason, message string, dryRun bool,
) map[string]string {
	labels := make(map[string]string, len(rem.Spec.Alert.Labels)+7)
	for k, v := range rem.Spec.Alert.Labels {
		labels[k] = v
	}

	labels[LabelEscalationRemediation] = rem.Name
	labels[LabelEscalationStrategy] = rem.Spec.StrategyName
	labels[LabelEscalationTarget] = rem.Spec.Target
	labels[LabelEscalationReason] = reason
	labels[LabelEscalationMessage] = message
	labels[LabelEscalationAttempts] = strconv.FormatInt(int64(attempts), 10)
	labels[LabelEscalationDryRun] = strconv.FormatBool(dryRun)
	return labels
}
