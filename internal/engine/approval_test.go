package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/action"
)

// awaiting builds a record in approval mode with a deadline ahead of the clock.
func awaiting(name string, deadlineIn time.Duration) *v1alpha1.Remediation {
	rem := remediation(name, v1alpha1.Step{Action: "deployment.restart"})
	rem.Spec.Mode = v1alpha1.ExecutionModeApproval
	deadline := metav1.NewTime(testClock.Add(deadlineIn))
	rem.Spec.ApprovalDeadline = &deadline
	rem.Spec.EscalationSteps = []v1alpha1.Step{{Action: "webhook.call"}}
	return rem
}

// Nothing is resolved, planned or executed while waiting. That is not an
// optimisation: a remediation waiting for approval must not already have worked
// out what it would do to a cluster that has since moved on.
func TestApproval_NothingRunsWhileWaiting(t *testing.T) {
	restart := &scriptedAction{name: "deployment.restart"}
	f := newReconciler(t, false, []action.Action{restart}, awaiting("rem-1", time.Hour))

	result, err := f.reconciler.Reconcile(context.Background(), request("rem-1"))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if restart.planCalls != 0 || restart.execCalls != 0 {
		t.Errorf("planned %d and executed %d while awaiting approval, want none",
			restart.planCalls, restart.execCalls)
	}
	if result.RequeueAfter == 0 {
		t.Error("no requeue; a missed watch event would hold this open past its deadline")
	}

	stored := f.client.stored(testNamespace, "rem-1")
	if stored.Status.State != v1alpha1.RemediationStateAwaitingApproval {
		t.Errorf("State = %q, want AwaitingApproval", stored.Status.State)
	}
	// The queue has to be visible, or it is a queue nobody empties.
	if !strings.Contains(stored.Status.Message, "waiting for approval") {
		t.Errorf("Message = %q, want it to say what it is waiting for", stored.Status.Message)
	}
	if !strings.Contains(stored.Status.Message, "escalates") {
		t.Errorf("Message = %q, want it to say what happens if nobody looks",
			stored.Status.Message)
	}
}

// Waiting is not Running, so a record found in Running still means only one
// thing: the process died.
func TestApproval_WaitingIsNotRunning(t *testing.T) {
	if v1alpha1.RemediationStateAwaitingApproval == v1alpha1.RemediationStateRunning {
		t.Fatal("AwaitingApproval must be its own state")
	}
	if v1alpha1.RemediationStateAwaitingApproval.IsTerminal() {
		t.Error("AwaitingApproval reported terminal; the decision is still to come")
	}
}

func TestApproval_ApprovingRunsIt(t *testing.T) {
	restart := &scriptedAction{name: "deployment.restart"}
	rem := awaiting("rem-1", time.Hour)
	rem.Spec.Approval = &v1alpha1.Approval{
		Decision: v1alpha1.ApprovalApprove, By: "dana",
	}

	f := newReconciler(t, false, []action.Action{restart}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if restart.execCalls != 1 {
		t.Fatalf("executed %d times, want 1", restart.execCalls)
	}
	stored := f.client.stored(testNamespace, "rem-1")
	if stored.Status.State != v1alpha1.RemediationStateSucceeded {
		t.Errorf("State = %q, want Succeeded", stored.Status.State)
	}
}

// A denial is terminal and quiet: somebody looked and said no, and telling them
// again is not information.
func TestApproval_DenyingEndsItWithoutEscalating(t *testing.T) {
	restart := &scriptedAction{name: "deployment.restart"}
	page := &scriptedAction{name: "webhook.call"}
	rem := awaiting("rem-1", time.Hour)
	rem.Spec.Approval = &v1alpha1.Approval{
		Decision: v1alpha1.ApprovalDeny, By: "dana", Note: "we are rolling forward",
	}

	f := newReconciler(t, false, []action.Action{restart, page}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if restart.execCalls != 0 {
		t.Errorf("executed %d times after a denial, want 0", restart.execCalls)
	}
	if page.execCalls != 0 {
		t.Errorf("escalated after a denial; somebody already looked and said no")
	}

	stored := f.client.stored(testNamespace, "rem-1")
	if stored.Status.Reason != v1alpha1.ReasonDenied {
		t.Errorf("Reason = %q, want %q", stored.Status.Reason, v1alpha1.ReasonDenied)
	}
	for _, want := range []string{"dana", "rolling forward"} {
		if !strings.Contains(stored.Status.Message, want) {
			t.Errorf("Message = %q, want it to contain %q", stored.Status.Message, want)
		}
	}
}

// The failure mode of a human gate is that nobody looks, and a gate that
// quietly drops what nobody looked at turns an alert into silence.
func TestApproval_TimingOutEscalates(t *testing.T) {
	restart := &scriptedAction{name: "deployment.restart"}
	page := &scriptedAction{name: "webhook.call"}
	// A deadline in the past: nobody decided.
	rem := awaiting("rem-1", -time.Minute)

	f := newReconciler(t, false, []action.Action{restart, page}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if restart.execCalls != 0 {
		t.Errorf("executed %d times after timing out, want 0", restart.execCalls)
	}
	if page.execCalls != 1 {
		t.Fatalf("escalated %d times, want 1: silence has to reach somebody", page.execCalls)
	}

	stored := f.client.stored(testNamespace, "rem-1")
	if stored.Status.Reason != v1alpha1.ReasonApprovalTimeout {
		t.Errorf("Reason = %q, want %q", stored.Status.Reason, v1alpha1.ReasonApprovalTimeout)
	}
	if stored.Status.Escalation == nil ||
		stored.Status.Escalation.Phase != v1alpha1.StepPhaseSucceeded {
		t.Errorf("escalation = %+v, want a recorded success", stored.Status.Escalation)
	}
}

// A record with no deadline would wait for ever, which is the one outcome a
// human gate must not have.
func TestApproval_NoDeadlineIsTreatedAsExpired(t *testing.T) {
	page := &scriptedAction{name: "webhook.call"}
	rem := awaiting("rem-1", time.Hour)
	rem.Spec.ApprovalDeadline = nil

	f := newReconciler(t, false, []action.Action{page}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	stored := f.client.stored(testNamespace, "rem-1")
	if !stored.Status.State.IsTerminal() {
		t.Errorf("State = %q; a record with no deadline would wait for ever",
			stored.Status.State)
	}
	if stored.Status.Reason != v1alpha1.ReasonApprovalTimeout {
		t.Errorf("Reason = %q, want %q", stored.Status.Reason, v1alpha1.ReasonApprovalTimeout)
	}
}

// Rewriting the same status on every requeue would make one waiting remediation
// a stream of watch events for every controller and dashboard in the namespace.
func TestApproval_WaitingDoesNotRewriteTheSameStatus(t *testing.T) {
	f := newReconciler(t, false, []action.Action{&scriptedAction{name: "deployment.restart"}},
		awaiting("rem-1", time.Hour))

	for range 5 {
		if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
	}

	if f.client.statusUpdates > 1 {
		t.Errorf("status written %d times over five reconciles, want 1",
			f.client.statusUpdates)
	}
}

// An auto-mode record is untouched by any of this.
func TestApproval_AutoModeIsUnaffected(t *testing.T) {
	restart := &scriptedAction{name: "deployment.restart"}
	rem := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	rem.Spec.Mode = v1alpha1.ExecutionModeAuto

	f := newReconciler(t, false, []action.Action{restart}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if restart.execCalls != 1 {
		t.Errorf("executed %d times in auto mode, want 1", restart.execCalls)
	}
}

// And a record with no mode at all, created before the field existed.
func TestApproval_AnEmptyModeRunsAsAuto(t *testing.T) {
	restart := &scriptedAction{name: "deployment.restart"}
	rem := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	rem.Spec.Mode = ""

	f := newReconciler(t, false, []action.Action{restart}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if restart.execCalls != 1 {
		t.Errorf("executed %d times with an empty mode, want 1: a record from "+
			"before the field existed must still run", restart.execCalls)
	}
}
