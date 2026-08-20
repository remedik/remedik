package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/action"
)

// strategyReconciler builds the controller under test with the actions a
// build would have registered.
func strategyReconciler(t *testing.T, c client.Client, actions ...string) *StrategyReconciler {
	t.Helper()

	registered := make([]action.Action, 0, len(actions))
	for _, name := range actions {
		registered = append(registered, &scriptedAction{name: name})
	}
	registry, err := action.NewRegistry(registered...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	return &StrategyReconciler{
		Client:    c,
		Registry:  registry,
		Namespace: "remedik",
		Logger:    slog.New(slog.DiscardHandler),
	}
}

func strategyFor(name string, steps []v1alpha1.Step, escalation []v1alpha1.Step) *v1alpha1.RemediationStrategy {
	return &v1alpha1.RemediationStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Generation: 3},
		Spec: v1alpha1.RemediationStrategySpec{
			Trigger:   v1alpha1.Trigger{Match: map[string]string{"alertname": "KubePodCrashLooping"}},
			Steps:     steps,
			OnFailure: v1alpha1.OnFailure{Steps: escalation},
		},
	}
}

func reconcileStrategy(t *testing.T, r *StrategyReconciler, name string) {
	t.Helper()

	if _, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: name}}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}

// readyCondition reads the condition back off the stored strategy, which is
// what a user's `kubectl get` and the dashboard both read.
func readyCondition(t *testing.T, c *fakeClient, name string) *metav1.Condition {
	t.Helper()

	stored := c.storedStrategy(name)
	if stored == nil {
		t.Fatalf("strategy %q is not stored", name)
	}
	return meta.FindStatusCondition(stored.Status.Conditions, v1alpha1.ConditionReady)
}

// A strategy naming an action this build does not have is the mistake this
// controller exists for: it is accepted by the API server and, without a
// condition, is discovered when a remediation fails at 03:00.
func TestStrategyReconciler_Readiness(t *testing.T) {
	restart := []v1alpha1.Step{{Action: "deployment.restart"}}

	tests := []struct {
		name       string
		steps      []v1alpha1.Step
		escalation []v1alpha1.Step
		enabled    *bool
		wantStatus metav1.ConditionStatus
		wantReason string
		wantInMsg  []string
	}{
		{
			name:       "every action is available",
			steps:      restart,
			wantStatus: metav1.ConditionTrue,
			wantReason: v1alpha1.ReasonUsable,
		},
		{
			name:       "a disabled strategy is still checked",
			steps:      restart,
			enabled:    ptr(false),
			wantStatus: metav1.ConditionTrue,
			wantReason: v1alpha1.ReasonUsable,
		},
		{
			name:       "a typo names the step and what is available",
			steps:      []v1alpha1.Step{{Action: "deployment.restart"}, {Action: "deployment.restrat"}},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ReasonUnknownAction,
			wantInMsg:  []string{"step 2", "deployment.restrat", "enabled actions: deployment.restart"},
		},
		{
			name:       "an action nobody enabled in the chart",
			steps:      []v1alpha1.Step{{Action: "node.drain"}},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ReasonUnknownAction,
			wantInMsg:  []string{"step 1", "node.drain"},
		},
		{
			// The half worth checking most: it is found out when a
			// remediation has already failed.
			name:       "an escalation that could never page anybody",
			steps:      restart,
			escalation: []v1alpha1.Step{{Action: "webhook.call"}},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ReasonUnknownAction,
			wantInMsg:  []string{"onFailure", "step 1", "webhook.call"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := strategyFor("pod-crashloop", tt.steps, tt.escalation)
			strategy.Spec.Enabled = tt.enabled

			c := newFakeClient(strategy)
			reconcileStrategy(t, strategyReconciler(t, c, "deployment.restart"), "pod-crashloop")

			ready := readyCondition(t, c, "pod-crashloop")
			if ready == nil {
				t.Fatal("no Ready condition was written")
			}
			if ready.Status != tt.wantStatus {
				t.Errorf("Ready = %s, want %s (message %q)", ready.Status, tt.wantStatus, ready.Message)
			}
			if ready.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", ready.Reason, tt.wantReason)
			}
			for _, want := range tt.wantInMsg {
				if !strings.Contains(ready.Message, want) {
					t.Errorf("message = %q, want it to contain %q", ready.Message, want)
				}
			}
			if ready.ObservedGeneration != 3 {
				t.Errorf("observedGeneration = %d, want 3", ready.ObservedGeneration)
			}
		})
	}
}

// Fixing the manifest has to clear the condition without an operator restart,
// or the report is worse than none: it would accuse a strategy that is fine.
func TestStrategyReconciler_RecoversWhenTheNameIsFixed(t *testing.T) {
	strategy := strategyFor("pod-crashloop", []v1alpha1.Step{{Action: "deployment.restrat"}}, nil)
	c := newFakeClient(strategy)
	r := strategyReconciler(t, c, "deployment.restart")

	reconcileStrategy(t, r, "pod-crashloop")
	if ready := readyCondition(t, c, "pod-crashloop"); ready.Status != metav1.ConditionFalse {
		t.Fatalf("Ready = %s, want False before the fix", ready.Status)
	}

	fixed := c.storedStrategy("pod-crashloop")
	fixed.Spec.Steps = []v1alpha1.Step{{Action: "deployment.restart"}}
	c.put(fixed)

	reconcileStrategy(t, r, "pod-crashloop")
	if ready := readyCondition(t, c, "pod-crashloop"); ready.Status != metav1.ConditionTrue {
		t.Errorf("Ready = %s, want True after the fix", ready.Status)
	}
}

// A status write is a watch event. A controller that writes on every pass
// reconciles itself forever, which is invisible in a cluster until somebody
// reads the audit log or the API server's request rate.
func TestStrategyReconciler_WritesNothingWhenNothingChanged(t *testing.T) {
	c := newFakeClient(strategyFor("pod-crashloop", []v1alpha1.Step{{Action: "deployment.restart"}}, nil))
	r := strategyReconciler(t, c, "deployment.restart")

	reconcileStrategy(t, r, "pod-crashloop")
	reconcileStrategy(t, r, "pod-crashloop")
	reconcileStrategy(t, r, "pod-crashloop")

	if c.statusUpdates != 1 {
		t.Errorf("status updates = %d, want 1", c.statusUpdates)
	}
}

// "Has this strategy ever fired?" is a question `kubectl get` should answer
// without a second query against the records.
func TestStrategyReconciler_CountsItsOwnRecords(t *testing.T) {
	objects := []client.Object{
		strategyFor("pod-crashloop", []v1alpha1.Step{{Action: "deployment.restart"}}, nil),
		strategyFor("node-not-ready", []v1alpha1.Step{{Action: "deployment.restart"}}, nil),
	}
	newest := testClock.Add(2 * time.Hour)
	for i, at := range []time.Time{testClock, newest, testClock.Add(time.Hour)} {
		objects = append(objects, record(fmt.Sprintf("pod-crashloop-%d", i), "pod-crashloop", at))
	}
	objects = append(objects, record("node-not-ready-0", "node-not-ready", newest.Add(time.Hour)))

	c := newFakeClient(objects...)
	reconcileStrategy(t, strategyReconciler(t, c, "deployment.restart"), "pod-crashloop")

	stored := c.storedStrategy("pod-crashloop")
	if stored.Status.ExecutionCount != 3 {
		t.Errorf("executionCount = %d, want 3 — the other strategy's record is not this one's",
			stored.Status.ExecutionCount)
	}
	if stored.Status.LastExecutionTime == nil || !stored.Status.LastExecutionTime.Time.Equal(newest) {
		t.Errorf("lastExecutionTime = %v, want %v", stored.Status.LastExecutionTime, newest)
	}
	if stored.Status.ObservedGeneration != 3 {
		t.Errorf("observedGeneration = %d, want 3", stored.Status.ObservedGeneration)
	}
}

// Retention prunes records, and the counter is derived from them, so it can
// go down. Pinned because it is the surprising half of that decision.
func TestStrategyReconciler_CountFollowsRetention(t *testing.T) {
	kept := record("pod-crashloop-0", "pod-crashloop", testClock)
	pruned := record("pod-crashloop-1", "pod-crashloop", testClock.Add(time.Hour))
	c := newFakeClient(
		strategyFor("pod-crashloop", []v1alpha1.Step{{Action: "deployment.restart"}}, nil), kept, pruned)
	r := strategyReconciler(t, c, "deployment.restart")

	reconcileStrategy(t, r, "pod-crashloop")
	if got := c.storedStrategy("pod-crashloop").Status.ExecutionCount; got != 2 {
		t.Fatalf("executionCount = %d, want 2", got)
	}

	if err := c.Delete(context.Background(), pruned); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	reconcileStrategy(t, r, "pod-crashloop")

	if got := c.storedStrategy("pod-crashloop").Status.ExecutionCount; got != 1 {
		t.Errorf("executionCount = %d, want 1 after the record was pruned", got)
	}
}

// A deleted strategy is an ordinary event: the records that name it outlive
// it, and there is nothing left to report on.
func TestStrategyReconciler_DeletedStrategyIsNotAnError(t *testing.T) {
	c := newFakeClient()
	r := strategyReconciler(t, c, "deployment.restart")

	if _, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "gone"}}); err != nil {
		t.Errorf("Reconcile() error = %v, want nil", err)
	}
	if c.statusUpdates != 0 {
		t.Errorf("status updates = %d, want 0", c.statusUpdates)
	}
}

// The count is derived, so a failed list must not be reported as zero: it is
// requeued instead.
func TestStrategyReconciler_ListFailureIsRequeued(t *testing.T) {
	c := newFakeClient(strategyFor("pod-crashloop", []v1alpha1.Step{{Action: "deployment.restart"}}, nil))
	c.listErr = errors.New("the cache is not started")

	_, err := strategyReconciler(t, c, "deployment.restart").Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: "pod-crashloop"}})
	if err == nil {
		t.Fatal("Reconcile() error = nil, want the list failure")
	}
	if c.statusUpdates != 0 {
		t.Errorf("status updates = %d, want 0 — nothing was known", c.statusUpdates)
	}
}

// The mapping is what makes a new record update the strategy it came from.
func TestStrategyOfRecord(t *testing.T) {
	labelled := record("pod-crashloop-0", "pod-crashloop", testClock)
	requests := strategyOfRecord(context.Background(), labelled)
	if len(requests) != 1 || requests[0].Name != "pod-crashloop" {
		t.Errorf("requests = %v, want one for pod-crashloop", requests)
	}
	if requests[0].Namespace != "" {
		t.Errorf("namespace = %q, want empty: strategies are cluster-scoped", requests[0].Namespace)
	}

	unlabelled := record("orphan", "", testClock)
	unlabelled.Labels = nil
	if requests := strategyOfRecord(context.Background(), unlabelled); requests != nil {
		t.Errorf("requests = %v, want none for a record with no strategy label", requests)
	}
}

func record(name, strategy string, created time.Time) *v1alpha1.Remediation {
	return &v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "remedik",
			CreationTimestamp: metav1.NewTime(created),
			Labels:            map[string]string{v1alpha1.LabelStrategy: strategy},
		},
		Spec: v1alpha1.RemediationSpec{StrategyName: strategy},
	}
}

func ptr[T any](v T) *T { return &v }
