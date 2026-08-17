package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/guards"
)

// A reconciler reads through the manager's cache, which is eventually
// consistent. So a second reconcile — triggered by the first one's own
// status write — can read a copy that still says Running after the first has
// already recorded Succeeded. By this operator's rule, Running means the
// process died, so that stale read decides the remediation was Interrupted.
//
// The only thing standing between that decision and a corrupted audit trail
// is optimistic concurrency: the stale object carries an old
// resourceVersion, so the write is refused. The conflict is not a defect to
// be smoothed over. It is the check.
//
// This was learned the hard way. Adding retry.RetryOnConflict to the status
// write — re-reading from the API server and re-applying the status the
// reconciler had already built — made the false Interrupted win, and turned
// a remediation whose every step succeeded into a Failed record. It was
// caught in an end-to-end run, where three log lines twenty-four
// milliseconds apart said "finished, state=Succeeded", then "was
// interrupted", then "status write needed more than one attempt".
//
// A retry here is only ever safe if it re-decides after re-reading, rather
// than re-applying a decision made from data that has since been overtaken.
// These tests exist so that anybody who tries again finds out here, and not
// from an incident.

// staleCacheClient serves reads from a snapshot that has fallen behind, and
// enforces resourceVersion on writes the way the API server does.
type staleCacheClient struct {
	*fakeClient

	// stale is what a Get returns: the object as the cache still has it.
	stale *v1alpha1.Remediation
}

func (c *staleCacheClient) Get(
	_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption,
) error {
	rem, ok := obj.(*v1alpha1.Remediation)
	if !ok || key.Name != c.stale.Name {
		return apierrors.NewNotFound(
			schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "remediations"},
			key.Name)
	}
	*rem = *c.stale.DeepCopy()
	return nil
}

func (c *staleCacheClient) Status() client.SubResourceWriter {
	return &versionedStatusWriter{client: c.fakeClient}
}

// versionedStatusWriter refuses a write whose resourceVersion is not the one
// currently stored, which is what the API server does.
type versionedStatusWriter struct {
	client.SubResourceWriter
	client *fakeClient
}

func (w *versionedStatusWriter) Update(
	_ context.Context, obj client.Object, _ ...client.SubResourceUpdateOption,
) error {
	w.client.mu.Lock()
	defer w.client.mu.Unlock()

	w.client.statusUpdates++

	key := keyOf(obj)
	stored, ok := w.client.objects[key]
	if !ok {
		return apierrors.NewNotFound(
			schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "remediations"},
			obj.GetName())
	}
	if obj.GetResourceVersion() != stored.GetResourceVersion() {
		return apierrors.NewConflict(
			schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "remediations"},
			obj.GetName(),
			errStaleWrite)
	}
	w.client.objects[key] = obj.DeepCopyObject().(client.Object)
	return nil
}

var errStaleWrite = apierrors.NewConflict(
	schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "remediations"},
	"", nil)

// staleReconciler wires a reconciler whose reads are behind its writes.
func staleReconciler(t *testing.T, current, stale *v1alpha1.Remediation) *RemediationReconciler {
	t.Helper()

	inner := newFakeClient(current)
	c := &staleCacheClient{fakeClient: inner, stale: stale}

	// If a future change gives the reconciler an uncached reader for retrying
	// conflicts, wire it here too — inner reads the store the way the API
	// server does. Without that, this test would go on passing while the
	// behaviour it guards was gone.
	return &RemediationReconciler{
		Client:  c,
		History: guards.NewMemoryHistory(0),
		Metrics: newCountingRecorder(),
		Logger:  quietLogger(),
		Now:     func() time.Time { return testClock },
	}
}

func TestReconcile_AStaleReadCannotOverwriteATerminalRecord(t *testing.T) {
	// What the API server holds: the execution finished, and it worked.
	current := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	current.ResourceVersion = "2"
	current.Status.State = v1alpha1.RemediationStateSucceeded

	// What the cache still says: mid-execution.
	stale := current.DeepCopy()
	stale.ResourceVersion = "1"
	stale.Status.State = v1alpha1.RemediationStateRunning

	r := staleReconciler(t, current, stale)

	// The stale read leads the reconciler to markInterrupted. The write must
	// be refused, and the refusal must reach the caller so the work is
	// requeued rather than dropped.
	_, err := r.Reconcile(context.Background(), request("rem-1"))

	// The harm first: what the record says is the product.
	stored := storedRemediation(r, "rem-1")
	if stored.Status.State != v1alpha1.RemediationStateSucceeded {
		t.Fatalf("stored state = %s, want Succeeded.\n\n"+
			"A stale read overwrote a remediation that had already finished, so a "+
			"remediation whose every step succeeded is now recorded as %s/%s. "+
			"Read the comment at the top of this file: the conflict this write "+
			"produces is the check, not a defect to retry away.",
			stored.Status.State, stored.Status.State, stored.Status.Reason)
	}
	if stored.Status.Reason == v1alpha1.ReasonInterrupted {
		t.Error("a completed remediation was recorded as Interrupted")
	}

	// Then the mechanism: the refusal has to reach the caller, so the work is
	// requeued rather than silently dropped.
	if err == nil {
		t.Fatal("Reconcile() error = nil; a write built from a stale read was accepted")
	}
	if !apierrors.IsConflict(errors.Unwrap(err)) && !apierrors.IsConflict(err) {
		t.Errorf("Reconcile() error = %v, want a conflict", err)
	}
}

// The next reconcile reads a fresh copy, sees a terminal state and stops.
// That is what makes the refused write harmless rather than a lost update.
func TestReconcile_TheNextPassSeesTheTerminalStateAndStops(t *testing.T) {
	current := remediation("rem-1", v1alpha1.Step{Action: "deployment.restart"})
	current.ResourceVersion = "2"
	current.Status.State = v1alpha1.RemediationStateSucceeded

	fresh := current.DeepCopy()
	r := staleReconciler(t, current, fresh)

	result, err := r.Reconcile(context.Background(), request("rem-1"))
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil for a terminal record", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("RequeueAfter = %v, want 0", result.RequeueAfter)
	}

	stored := storedRemediation(r, "rem-1")
	if stored.Status.State != v1alpha1.RemediationStateSucceeded {
		t.Errorf("stored state = %s, want Succeeded untouched", stored.Status.State)
	}
}

func storedRemediation(r *RemediationReconciler, name string) *v1alpha1.Remediation {
	c := r.Client.(*staleCacheClient)
	return c.stored(testNamespace, name)
}
