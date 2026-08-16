package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/ratyx/remedik/api/v1alpha1"
	"github.com/ratyx/remedik/internal/action"
)

// escalating builds a record whose remediation fails and whose onFailure plan
// calls a "webhook.call" that the test controls.
func escalating(retries int32) *v1alpha1.Remediation {
	rem := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	rem.Spec.Retries = retries
	rem.Spec.EscalationSteps = []v1alpha1.Step{{Action: "webhook.call"}}
	return rem
}

func TestEscalation_RunsOnceRetriesAreExhausted(t *testing.T) {
	broken := &scriptedAction{name: "deployment.restart", execErr: errors.New("still broken")}
	page := &scriptedAction{name: "webhook.call"}
	f := newReconciler(t, false, []action.Action{broken, page}, escalating(0))

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	rem := f.client.stored(testNamespace, "rem-1")

	// The remediation failed, and escalating did not soften that.
	if rem.Status.State != v1alpha1.RemediationStateFailed {
		t.Errorf("State = %q, want Failed: escalating is not succeeding", rem.Status.State)
	}
	if page.execCalls != 1 {
		t.Fatalf("the escalation ran %d times, want 1", page.execCalls)
	}

	if rem.Status.Escalation == nil {
		t.Fatal("the record does not say whether anybody was told")
	}
	if rem.Status.Escalation.Phase != v1alpha1.StepPhaseSucceeded {
		t.Errorf("escalation phase = %q, want Succeeded", rem.Status.Escalation.Phase)
	}
	if len(rem.Status.Escalation.Steps) != 1 {
		t.Errorf("escalation steps = %+v, want one", rem.Status.Escalation.Steps)
	}
	if rem.Status.Escalation.CompletedAt == nil {
		t.Error("the escalation has no completion time")
	}
	// Kept apart from the remediation's own steps, so a page does not read
	// as a fourth attempt at the restart.
	if len(rem.Status.Steps) != 1 || rem.Status.Steps[0].Action != "deployment.restart" {
		t.Errorf("steps = %+v, want only the remediation's own", rem.Status.Steps)
	}
	if f.metrics.escalated["Succeeded"] != 1 {
		t.Errorf("escalation metrics = %v, want one Succeeded", f.metrics.escalated)
	}
}

// The point of escalating only once retries are spent: paging on the first of
// three attempts pages for something about to fix itself, and a page that is
// usually unnecessary is a page people learn to ignore.
func TestEscalation_DoesNotFireBetweenRetries(t *testing.T) {
	broken := &scriptedAction{name: "deployment.restart", execErr: errors.New("still broken")}
	page := &scriptedAction{name: "webhook.call"}
	f := newReconciler(t, false, []action.Action{broken, page}, escalating(2))

	got, err := f.reconciler.Reconcile(context.Background(), request("rem-1"))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got.RequeueAfter == 0 {
		t.Fatal("the first failure of three did not schedule a retry")
	}
	if page.execCalls != 0 {
		t.Fatalf("escalated after attempt 1 of 3: %d calls, want 0", page.execCalls)
	}
	if f.client.stored(testNamespace, "rem-1").Status.Escalation != nil {
		t.Error("a retryable failure recorded an escalation")
	}

	// Attempts two and three: still no page until the budget is gone.
	for attempt := 2; attempt <= 3; attempt++ {
		if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
			t.Fatalf("attempt %d: Reconcile() error = %v", attempt, err)
		}
	}

	if page.execCalls != 1 {
		t.Errorf("the escalation ran %d times over three attempts, want exactly 1", page.execCalls)
	}
	if broken.execCalls != 3 {
		t.Errorf("the remediation ran %d times, want 3", broken.execCalls)
	}
}

// Escalation is the one thing that runs for real in a dry run, because a
// trial where the escalation path is untested proved half of what it should.
func TestEscalation_RunsForRealInDryRun(t *testing.T) {
	broken := &scriptedAction{name: "deployment.restart", planErr: errors.New("would not work")}
	page := &scriptedAction{name: "webhook.call"}
	f := newReconciler(t, true, []action.Action{broken, page}, escalating(0))

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if page.execCalls != 1 || page.planCalls != 0 {
		t.Errorf("escalation in dry-run: %d executes and %d plans, want 1 and 0",
			page.execCalls, page.planCalls)
	}
	if page.seenDryRun {
		t.Error("the escalation was simulated; it must run for real")
	}
	// And it is told, so it can say so rather than paging for an incident
	// that did not happen.
	if got := page.seenLabels[LabelEscalationDryRun]; got != "true" {
		t.Errorf("%s = %q, want \"true\"", LabelEscalationDryRun, got)
	}
}

func TestEscalation_IsToldWhatFailed(t *testing.T) {
	broken := &scriptedAction{name: "deployment.restart", execErr: errors.New("still broken")}
	page := &scriptedAction{name: "webhook.call"}
	rem := escalating(1)
	// An alert label that would shadow remedik's own, if remedik let it.
	rem.Spec.Alert.Labels[LabelEscalationReason] = "nothing is wrong at all"
	f := newReconciler(t, false, []action.Action{broken, page}, rem)

	for i := 0; i < 2; i++ {
		if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
	}
	if page.execCalls != 1 {
		t.Fatalf("the escalation ran %d times, want 1", page.execCalls)
	}

	want := map[string]string{
		LabelEscalationRemediation: "rem-1",
		LabelEscalationStrategy:    "restart-api",
		LabelEscalationTarget:      "deployment/payments/api",
		LabelEscalationReason:      v1alpha1.ReasonStepFailed,
		LabelEscalationMessage:     "execute deployment.restart on deployment/payments/api: still broken",
		LabelEscalationAttempts:    "2",
		LabelEscalationDryRun:      "false",
	}
	for key, wantValue := range want {
		if got := page.seenLabels[key]; got != wantValue {
			t.Errorf("%s = %q, want %q", key, got, wantValue)
		}
	}
	// The alert's own labels still reach the escalation.
	if page.seenLabels["namespace"] == "" {
		t.Error("the alert's labels did not reach the escalation")
	}
}

// A page that could not be sent is the thing somebody most needs to find
// later, so it is recorded — and it changes nothing, because the remediation
// had already failed and there is no worse state to move to.
func TestEscalation_FailureIsRecordedAndChangesNothing(t *testing.T) {
	broken := &scriptedAction{name: "deployment.restart", execErr: errors.New("still broken")}
	page := &scriptedAction{name: "webhook.call", execErr: errors.New("pagerduty returned 503")}
	f := newReconciler(t, false, []action.Action{broken, page}, escalating(0))

	got, err := f.reconciler.Reconcile(context.Background(), request("rem-1"))
	if err != nil {
		t.Fatalf("a failed escalation became a reconcile error: %v", err)
	}
	if got.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v: a failed page must not re-run the remediation", got.RequeueAfter)
	}

	rem := f.client.stored(testNamespace, "rem-1")
	if rem.Status.State != v1alpha1.RemediationStateFailed {
		t.Errorf("State = %q, want Failed", rem.Status.State)
	}
	// The remediation's own reason must still be the remediation's, not the
	// page's: whoever reads this needs to know the restart failed first.
	if rem.Status.Reason != v1alpha1.ReasonStepFailed {
		t.Errorf("Reason = %q, want %q", rem.Status.Reason, v1alpha1.ReasonStepFailed)
	}
	if rem.Status.Message != "execute deployment.restart on deployment/payments/api: still broken" {
		t.Errorf("Message = %q, want the remediation's failure", rem.Status.Message)
	}

	if rem.Status.Escalation == nil || rem.Status.Escalation.Phase != v1alpha1.StepPhaseFailed {
		t.Fatalf("escalation = %+v, want a recorded failure", rem.Status.Escalation)
	}
	if rem.Status.Escalation.Message == "" {
		t.Error("a failed escalation did not say why")
	}
	if f.metrics.escalated["Failed"] != 1 {
		t.Errorf("escalation metrics = %v, want one Failed", f.metrics.escalated)
	}
}

func TestEscalation_AbsentWhenTheStrategyDeclaresNone(t *testing.T) {
	broken := &scriptedAction{name: "deployment.restart", execErr: errors.New("still broken")}
	rem := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	f := newReconciler(t, false, []action.Action{broken}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := f.client.stored(testNamespace, "rem-1").Status.Escalation; got != nil {
		t.Errorf("escalation = %+v, want none when the strategy declares none", got)
	}
	if len(f.metrics.escalated) != 0 {
		t.Errorf("escalation metrics = %v, want none", f.metrics.escalated)
	}
}

// A successful remediation never escalates, however loud the onFailure plan.
func TestEscalation_NotOnSuccess(t *testing.T) {
	fine := &scriptedAction{name: "deployment.restart"}
	page := &scriptedAction{name: "webhook.call"}
	f := newReconciler(t, false, []action.Action{fine, page}, escalating(0))

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if page.execCalls != 0 {
		t.Errorf("a successful remediation escalated %d times", page.execCalls)
	}
}

// An unknown action in the escalation plan fails the escalation and nothing
// else — the remediation's own outcome is already decided.
func TestEscalation_UnknownActionFailsOnlyTheEscalation(t *testing.T) {
	broken := &scriptedAction{name: "deployment.restart", execErr: errors.New("still broken")}
	rem := escalating(0)
	rem.Spec.EscalationSteps = []v1alpha1.Step{{Action: "pagerduty.notify"}}
	f := newReconciler(t, false, []action.Action{broken}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	stored := f.client.stored(testNamespace, "rem-1")
	if stored.Status.State != v1alpha1.RemediationStateFailed {
		t.Errorf("State = %q, want Failed", stored.Status.State)
	}
	if stored.Status.Reason != v1alpha1.ReasonStepFailed {
		t.Errorf("Reason = %q, want the remediation's own", stored.Status.Reason)
	}
	if stored.Status.Escalation == nil || stored.Status.Escalation.Phase != v1alpha1.StepPhaseFailed {
		t.Errorf("escalation = %+v, want a recorded failure", stored.Status.Escalation)
	}
}
