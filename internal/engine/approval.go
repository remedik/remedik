package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/remedik/remedik/api/v1alpha1"
)

// approvalPollFloor bounds how long the reconciler will sit on a waiting
// record before looking again.
//
// The wait is not a poll: the controller watches Remediation resources, so a
// decision is a watch event and is acted on in about a second. This exists
// only so that a missed event cannot hold a remediation open past its deadline
// — a requeue is cheap and a gate that never times out is the failure this
// whole feature is built to avoid.
const approvalPollFloor = time.Minute

// awaitApproval holds a remediation until somebody decides, or until nobody
// does for long enough.
//
// Nothing is resolved, planned or executed here. That is the point: a
// remediation waiting for approval must not already have worked out what it
// would do to a cluster that has since moved on, so the plan is produced after
// the decision, against the cluster as it is then.
func (r *RemediationReconciler) awaitApproval(
	ctx context.Context, rem *v1alpha1.Remediation, log *slog.Logger,
) (ctrl.Result, error) {
	decision := rem.Spec.Approval

	switch {
	case decision != nil && decision.Decision == v1alpha1.ApprovalApprove:
		log.Info("approved; running", "by", claimedBy(decision))
		r.recordApproval(rem, decision, true)
		// Falls through to the ordinary path below, which resolves and plans
		// now rather than at creation.
		return r.runAttempt(ctx, rem, log)

	case decision != nil && decision.Decision == v1alpha1.ApprovalDeny:
		log.Info("denied; nothing will run", "by", claimedBy(decision), "note", decision.Note)
		r.recordApproval(rem, decision, false)
		// Deliberately no escalation. Somebody looked and said no; telling them
		// again is not information.
		return ctrl.Result{}, r.finish(ctx, rem,
			v1alpha1.RemediationStateFailed, v1alpha1.ReasonDenied,
			deniedMessage(decision), log)
	}

	// Nobody has decided. Either wait, or stop waiting.
	deadline := rem.Spec.ApprovalDeadline
	if deadline == nil {
		// A record in this state with no deadline cannot be waited on
		// meaningfully — it would wait for ever, which is the one outcome a
		// human gate must not have. Treat it as timed out immediately and say
		// so, rather than silently holding it.
		log.Warn("awaiting approval with no deadline; treating it as expired")
		return ctrl.Result{}, r.timeOutApproval(ctx, rem, log)
	}

	now := r.now()
	if now.Before(deadline.Time) {
		left := deadline.Sub(now)
		log.Info("awaiting approval", "deadline", deadline.Time, "left", left.Round(time.Second))

		if err := r.markAwaitingApproval(ctx, rem, left, log); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue a little past the deadline, so the timeout is decided by the
		// clock rather than by whether a requeue landed early.
		return ctrl.Result{RequeueAfter: min(left+time.Second, approvalPollFloor)}, nil
	}

	return ctrl.Result{}, r.timeOutApproval(ctx, rem, log)
}

// markAwaitingApproval records the wait, so that `kubectl get remediations` and
// the dashboard both show a queue somebody has to empty.
//
// A queue nobody can see is a queue nobody empties, and an approval gate that
// silently accumulates is worse than none: it looks like remediation working.
func (r *RemediationReconciler) markAwaitingApproval(
	ctx context.Context, rem *v1alpha1.Remediation, left time.Duration, log *slog.Logger,
) error {
	message := fmt.Sprintf("waiting for approval; %s left before this escalates",
		left.Round(time.Second))

	if rem.Status.State == v1alpha1.RemediationStateAwaitingApproval &&
		rem.Status.Message == message {
		// Nothing changed worth a write. Rewriting the same status on every
		// requeue would make one waiting remediation a stream of watch events
		// for every controller and dashboard reading this namespace.
		return nil
	}

	rem.Status.State = v1alpha1.RemediationStateAwaitingApproval
	rem.Status.Reason = ""
	rem.Status.Message = message

	// Not retried on conflict, for the reason invariant 3 gives: a conflict
	// here means another pass already moved this record on, and overwriting
	// that with a decision made from a stale read is the defect
	// staleread_test.go exists to refuse.
	if err := r.Client.Status().Update(ctx, rem); err != nil {
		return fmt.Errorf("record awaiting approval: %w", err)
	}
	log.Debug("recorded that this is waiting for approval")
	return nil
}

// timeOutApproval ends a remediation nobody decided on, and escalates.
func (r *RemediationReconciler) timeOutApproval(
	ctx context.Context, rem *v1alpha1.Remediation, log *slog.Logger,
) error {
	message := "nobody approved or denied this in time, so it was not run"

	log.Warn("approval timed out; escalating instead of running")

	// Escalation before the terminal write, as everywhere else, so the record
	// reaches its final state with the outcome of the page already in it.
	rem.Status.Escalation = r.escalate(ctx, rem, 0,
		v1alpha1.ReasonApprovalTimeout, message, log)

	return r.finish(ctx, rem,
		v1alpha1.RemediationStateFailed, v1alpha1.ReasonApprovalTimeout, message, log)
}

// recordApproval publishes the decision as an event on the remediation, so it
// appears in `kubectl describe` beside the steps.
//
// The claim is published as a claim. remedik cannot authenticate a patch, and
// the cluster's audit log is the authority on who issued it.
func (r *RemediationReconciler) recordApproval(
	rem *v1alpha1.Remediation, decision *v1alpha1.Approval, approved bool,
) {
	if r.Events == nil {
		return
	}
	reason := EventReasonDenied
	verb := "denied"
	if approved {
		reason = EventReasonApproved
		verb = "approved"
	}
	r.Events.Eventf(rem, nil, corev1.EventTypeNormal, reason, string(decision.Decision),
		"%s by %s%s", verb, claimedBy(decision), noteSuffix(decision.Note))
}

// Event reasons for the two human decisions.
const (
	// EventReasonApproved is published when somebody lets a remediation run.
	EventReasonApproved = "Approved"
	// EventReasonDenied is published when somebody stops one.
	EventReasonDenied = "Denied"
)

func claimedBy(decision *v1alpha1.Approval) string {
	if decision == nil || decision.By == "" {
		// Said this way rather than left blank: "approved by" with nothing
		// after it reads like a bug, and "somebody who did not say" is the
		// truth. The cluster's audit log has the answer.
		return "somebody who did not say who"
	}
	return decision.By
}

func deniedMessage(decision *v1alpha1.Approval) string {
	message := fmt.Sprintf("denied by %s", claimedBy(decision))
	if decision.Note != "" {
		message += ": " + decision.Note
	}
	return message
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return " — " + note
}

// awaitsApproval reports whether this record still needs a human before
// anything runs.
func awaitsApproval(rem *v1alpha1.Remediation) bool {
	if rem.Spec.Mode != v1alpha1.ExecutionModeApproval {
		return false
	}
	// An approved remediation goes through the ordinary path on the next
	// reconcile, so it is only "awaiting" while there is no decision — and
	// while it has not already reached a terminal state, which a denial or a
	// timeout leaves behind.
	return !rem.Status.State.IsTerminal()
}
