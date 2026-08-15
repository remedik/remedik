package engine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/api/v1alpha1"
	"github.com/ratyx/remedik/internal/action"
	"github.com/ratyx/remedik/internal/guards"
)

const testNamespace = "remedik"

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// countingRecorder captures engine telemetry.
type countingRecorder struct {
	unmatched int
	rejected  map[string]int
	started   int
	finished  map[string]int
}

func newCountingRecorder() *countingRecorder {
	return &countingRecorder{rejected: map[string]int{}, finished: map[string]int{}}
}

func (r *countingRecorder) Unmatched()                                 { r.unmatched++ }
func (r *countingRecorder) GuardRejected(_, guard string)              { r.rejected[guard]++ }
func (r *countingRecorder) RemediationStarted(string)                  { r.started++ }
func (r *countingRecorder) RemediationFinished(_, o string, _ float64) { r.finished[o]++ }

// remediation builds a record in the state a freshly created one has.
func remediation(name string, steps ...v1alpha1.Step) *v1alpha1.Remediation {
	return &v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{v1alpha1.LabelStrategy: "restart-api"},
		},
		Spec: v1alpha1.RemediationSpec{
			StrategyName: "restart-api",
			Target:       "deployment/payments/api",
			Alert: v1alpha1.AlertRef{
				Fingerprint: "f1",
				Name:        "KubePodCrashLooping",
				Labels:      map[string]string{"namespace": "payments", "deployment": "api"},
			},
			Steps: steps,
		},
		Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStatePending},
	}
}

type reconcilerFixture struct {
	reconciler *RemediationReconciler
	client     *fakeClient
	metrics    *countingRecorder
	history    *guards.MemoryHistory
}

func newReconciler(t *testing.T, dryRun bool, actions []action.Action, objs ...client.Object) *reconcilerFixture {
	t.Helper()

	registry, err := action.NewRegistry(actions...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	c := newFakeClient(objs...)
	metrics := newCountingRecorder()
	history := guards.NewMemoryHistory(0)

	return &reconcilerFixture{
		client:  c,
		metrics: metrics,
		history: history,
		reconciler: &RemediationReconciler{
			Client:   c,
			Registry: registry,
			History:  history,
			DryRun:   dryRun,
			Metrics:  metrics,
			Logger:   quietLogger(),
			Now:      func() time.Time { return testClock },
		},
	}
}

func request(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKey{Namespace: testNamespace, Name: name}}
}

func TestReconcile_SuccessfulExecution(t *testing.T) {
	a := &scriptedAction{name: "deployment.restart"}
	f := newReconciler(t, false, []action.Action{a},
		remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"}))

	got, err := f.reconciler.Reconcile(context.Background(), request("rem-1"))
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}
	if got.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0 for a finished execution", got.RequeueAfter)
	}

	rem := f.client.stored(testNamespace, "rem-1")
	if rem.Status.State != v1alpha1.RemediationStateSucceeded {
		t.Errorf("State = %q, want Succeeded", rem.Status.State)
	}
	if rem.Status.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", rem.Status.Attempt)
	}
	if rem.Status.StartedAt == nil || rem.Status.CompletedAt == nil {
		t.Error("terminal record is missing timestamps")
	}
	if len(rem.Status.Steps) != 1 || rem.Status.Steps[0].Phase != v1alpha1.StepPhaseSucceeded {
		t.Errorf("steps = %+v, want one succeeded step", rem.Status.Steps)
	}
	if a.execCalls != 1 {
		t.Errorf("the action ran %d times, want 1", a.execCalls)
	}
	if f.metrics.finished["Succeeded"] != 1 {
		t.Errorf("finished metrics = %v, want one Succeeded", f.metrics.finished)
	}

	// The cooldown guard must see this completion.
	if _, ok := f.history.LastCompletion("restart-api", "deployment/payments/api"); !ok {
		t.Error("the completion was not recorded in guard history")
	}
}

func TestReconcile_DryRunProducesSimulatedRecord(t *testing.T) {
	a := &scriptedAction{name: "deployment.restart"}
	f := newReconciler(t, true, []action.Action{a},
		remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"}))

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	rem := f.client.stored(testNamespace, "rem-1")
	if rem.Status.State != v1alpha1.RemediationStateSimulated {
		t.Errorf("State = %q, want Simulated", rem.Status.State)
	}
	if a.execCalls != 0 {
		t.Errorf("Execute ran %d times in dry-run", a.execCalls)
	}
	if a.planCalls != 1 {
		t.Errorf("Plan ran %d times, want 1", a.planCalls)
	}
	// The record must carry the plan that would have run: that is the whole
	// value of a dry-run report.
	if rem.Status.Steps[0].Plan == "" {
		t.Error("the simulated step records no plan")
	}
}

// A Remediation found in Running can only mean the operator died
// mid-execution: a healthy attempt never returns while Running.
func TestReconcile_InterruptedExecution(t *testing.T) {
	rem := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	rem.Status.State = v1alpha1.RemediationStateRunning
	rem.Status.Attempt = 1
	rem.Status.Steps = []v1alpha1.StepStatus{
		{Index: 0, Action: "deployment.restart", Phase: v1alpha1.StepPhaseRunning},
	}
	a := &scriptedAction{name: "deployment.restart"}
	f := newReconciler(t, false, []action.Action{a}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	got := f.client.stored(testNamespace, "rem-1")
	if got.Status.State != v1alpha1.RemediationStateFailed {
		t.Errorf("State = %q, want Failed", got.Status.State)
	}
	if got.Status.Reason != v1alpha1.ReasonInterrupted {
		t.Errorf("Reason = %q, want %q", got.Status.Reason, v1alpha1.ReasonInterrupted)
	}
	// Crucially: the step must not be re-run.
	if a.execCalls != 0 {
		t.Errorf("the action ran %d times while recovering; steps must never be silently repeated", a.execCalls)
	}
	if got.Status.Steps[0].Phase != v1alpha1.StepPhaseFailed {
		t.Errorf("the in-flight step is still %q, want Failed", got.Status.Steps[0].Phase)
	}
}

func TestReconcile_RetriesThenSucceeds(t *testing.T) {
	rem := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	rem.Spec.Retries = 1
	a := &scriptedAction{name: "deployment.restart", execErr: errors.New("conflict")}
	f := newReconciler(t, false, []action.Action{a}, rem)

	// First attempt fails and asks to be retried after a backoff.
	got, err := f.reconciler.Reconcile(context.Background(), request("rem-1"))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got.RequeueAfter != Backoff(2) {
		t.Errorf("RequeueAfter = %v, want %v", got.RequeueAfter, Backoff(2))
	}

	after := f.client.stored(testNamespace, "rem-1")
	// Waiting for a retry must be Pending, not Running: otherwise a restart
	// during the wait would be misread as an interruption.
	if after.Status.State != v1alpha1.RemediationStatePending {
		t.Errorf("State = %q, want Pending while waiting to retry", after.Status.State)
	}
	if after.Status.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", after.Status.Attempt)
	}
	if after.Status.Message == "" {
		t.Error("the failed attempt left no message")
	}

	// Second attempt succeeds.
	a.execErr = nil
	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	final := f.client.stored(testNamespace, "rem-1")
	if final.Status.State != v1alpha1.RemediationStateSucceeded {
		t.Errorf("State = %q, want Succeeded", final.Status.State)
	}
	if final.Status.Attempt != 2 {
		t.Errorf("Attempt = %d, want 2", final.Status.Attempt)
	}
	if final.Status.Reason != "" {
		t.Errorf("Reason = %q, want it cleared on success", final.Status.Reason)
	}
}

func TestReconcile_FailsWhenRetriesExhausted(t *testing.T) {
	rem := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	rem.Spec.Retries = 0
	a := &scriptedAction{name: "deployment.restart", execErr: errors.New("still broken")}
	f := newReconciler(t, false, []action.Action{a}, rem)

	got, err := f.reconciler.Reconcile(context.Background(), request("rem-1"))
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want no retry", got.RequeueAfter)
	}

	final := f.client.stored(testNamespace, "rem-1")
	if final.Status.State != v1alpha1.RemediationStateFailed {
		t.Errorf("State = %q, want Failed", final.Status.State)
	}
	if final.Status.Reason != v1alpha1.ReasonStepFailed {
		t.Errorf("Reason = %q, want %q", final.Status.Reason, v1alpha1.ReasonStepFailed)
	}
	if f.metrics.finished["Failed"] != 1 {
		t.Errorf("finished metrics = %v, want one Failed", f.metrics.finished)
	}
	// A failed remediation still starts the cooldown: retrying immediately
	// on the next alert is exactly the loop cooldown exists to prevent.
	if _, ok := f.history.LastCompletion("restart-api", "deployment/payments/api"); !ok {
		t.Error("a failed execution did not start the cooldown")
	}
}

func TestReconcile_TerminalRecordIsLeftAlone(t *testing.T) {
	rem := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	rem.Status.State = v1alpha1.RemediationStateSucceeded
	a := &scriptedAction{name: "deployment.restart"}
	f := newReconciler(t, false, []action.Action{a}, rem)

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if updates, _ := f.client.counters(); updates != 0 {
		t.Errorf("a terminal record was written %d times", updates)
	}
	if a.execCalls != 0 {
		t.Errorf("a terminal record ran its action %d times", a.execCalls)
	}
}

func TestReconcile_DeletedRecordIsNotAnError(t *testing.T) {
	f := newReconciler(t, false, []action.Action{&scriptedAction{name: "deployment.restart"}})

	got, err := f.reconciler.Reconcile(context.Background(), request("gone"))
	if err != nil {
		t.Errorf("Reconcile() error = %v, want nil for a deleted record", err)
	}
	if got.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0", got.RequeueAfter)
	}
}

func TestReconcile_UnknownActionFailsTheRecord(t *testing.T) {
	f := newReconciler(t, false, []action.Action{&scriptedAction{name: "deployment.restart"}},
		remediation("rem-1", v1alpha1.Step{Action: "deployment.restrt"}))

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-1")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	final := f.client.stored(testNamespace, "rem-1")
	if final.Status.Reason != v1alpha1.ReasonUnknownAction {
		t.Errorf("Reason = %q, want %q", final.Status.Reason, v1alpha1.ReasonUnknownAction)
	}
	if final.Status.State != v1alpha1.RemediationStateFailed {
		t.Errorf("State = %q, want Failed", final.Status.State)
	}
}

func TestReconcile_PrunesOldTerminalRecords(t *testing.T) {
	current := remediation("rem-current", v1alpha1.Step{Action: "deployment.restart"})
	current.CreationTimestamp = metav1.NewTime(testClock)
	f := newReconciler(t, false, []action.Action{&scriptedAction{name: "deployment.restart"}}, current)
	f.reconciler.HistoryLimit = 3

	// Five older terminal records for the same strategy.
	for i := range 5 {
		old := remediation("old-"+string(rune('a'+i)), v1alpha1.Step{Action: "deployment.restart"})
		old.Status.State = v1alpha1.RemediationStateSucceeded
		old.CreationTimestamp = metav1.NewTime(testClock.Add(-time.Duration(i+1) * time.Hour))
		if err := f.client.Create(context.Background(), old); err != nil {
			t.Fatalf("seed error = %v", err)
		}
	}
	// A record from another strategy must be untouched.
	other := remediation("other-1", v1alpha1.Step{Action: "deployment.restart"})
	other.Name = "other-1"
	other.Spec.StrategyName = "other"
	other.Labels[v1alpha1.LabelStrategy] = "other"
	other.Status.State = v1alpha1.RemediationStateSucceeded
	if err := f.client.Create(context.Background(), other); err != nil {
		t.Fatalf("seed error = %v", err)
	}

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-current")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	var kept int
	for _, rem := range f.client.remediations() {
		if rem.Spec.StrategyName == "restart-api" && rem.Status.State.IsTerminal() {
			kept++
		}
	}
	if kept != 3 {
		t.Errorf("kept %d terminal records, want the limit of 3", kept)
	}
	if f.client.stored(testNamespace, "other-1") == nil {
		t.Error("pruning deleted another strategy's record")
	}
	// The newest survivor must be the one just completed.
	if rem := f.client.stored(testNamespace, "rem-current"); rem == nil {
		t.Error("pruning deleted the record that just completed")
	}
}

// Pruning is housekeeping: if it fails, the remediation is still finished
// and must not be requeued as if the execution itself had failed.
func TestReconcile_PruneFailureDoesNotFailTheRemediation(t *testing.T) {
	f := newReconciler(t, false, []action.Action{&scriptedAction{name: "deployment.restart"}},
		remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"}))
	f.client.listErr = errors.New("api server unavailable")

	got, err := f.reconciler.Reconcile(context.Background(), request("rem-1"))
	if err != nil {
		t.Errorf("Reconcile() error = %v, want nil despite the prune failure", err)
	}
	if got.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0", got.RequeueAfter)
	}
	if rem := f.client.stored(testNamespace, "rem-1"); rem.Status.State != v1alpha1.RemediationStateSucceeded {
		t.Errorf("State = %q, want Succeeded", rem.Status.State)
	}
}

func TestReconcile_StatusWriteFailureIsRetried(t *testing.T) {
	f := newReconciler(t, false, []action.Action{&scriptedAction{name: "deployment.restart"}},
		remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"}))
	f.client.statusUpdateErr = errors.New("conflict")

	_, err := f.reconciler.Reconcile(context.Background(), request("rem-1"))
	if err == nil {
		t.Fatal("Reconcile() error = nil; a failed status write must surface so the work is requeued")
	}
}

// Whatever the timestamps say, the record being finished must survive: it
// is the entry an operator goes looking for first.
func TestReconcile_PruneNeverDeletesTheRecordJustFinished(t *testing.T) {
	current := remediation("rem-current", v1alpha1.Step{Action: "deployment.restart"})
	// Deliberately the oldest by creation time — a clock-skew scenario.
	current.CreationTimestamp = metav1.NewTime(testClock.Add(-24 * time.Hour))

	f := newReconciler(t, false, []action.Action{&scriptedAction{name: "deployment.restart"}}, current)
	f.reconciler.HistoryLimit = 1

	for i := range 3 {
		old := remediation("newer-"+string(rune('a'+i)), v1alpha1.Step{Action: "deployment.restart"})
		old.Status.State = v1alpha1.RemediationStateSucceeded
		old.CreationTimestamp = metav1.NewTime(testClock)
		if err := f.client.Create(context.Background(), old); err != nil {
			t.Fatalf("seed error = %v", err)
		}
	}

	if _, err := f.reconciler.Reconcile(context.Background(), request("rem-current")); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if f.client.stored(testNamespace, "rem-current") == nil {
		t.Fatal("the record that just finished was pruned")
	}
	var kept int
	for _, rem := range f.client.remediations() {
		if rem.Status.State.IsTerminal() {
			kept++
		}
	}
	if kept != 1 {
		t.Errorf("kept %d terminal records, want the limit of 1", kept)
	}
}
