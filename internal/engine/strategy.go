package engine

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/action"
)

// StrategyReconciler keeps a RemediationStrategy's status true.
//
// A strategy is the one object in remedik that its user writes, and until
// this controller existed it was also the only one that never answered back.
// A strategy naming an action the chart did not enable was accepted by the
// API server, looked correct in `kubectl get`, and was discovered at 03:00 as
// a Remediation that failed with UnknownAction. The check already existed;
// what was missing was something to run it when the manifest was applied
// rather than when it mattered.
//
// It executes nothing, reads no workloads and holds no state. Its whole job
// is to answer two questions on the resource itself: is this strategy usable,
// and is it being used.
type StrategyReconciler struct {
	// Client reads strategies and their records, and writes status.
	Client client.Client
	// Registry is what "usable" means: the actions this build can run,
	// which is the set the chart enabled.
	Registry *action.Registry
	// Namespace is where Remediation records live.
	Namespace string
	// Logger is required.
	Logger *slog.Logger
}

// Reconcile implements reconcile.Reconciler.
func (r *StrategyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var strategy v1alpha1.RemediationStrategy
	if err := r.Client.Get(ctx, req.NamespacedName, &strategy); err != nil {
		// A deleted strategy is not an error, and it has no status left to
		// keep: its records outlive it and explain themselves.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var records v1alpha1.RemediationList
	if err := r.Client.List(ctx, &records,
		client.InNamespace(r.Namespace),
		client.MatchingLabels{v1alpha1.LabelStrategy: strategy.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("list the records of %s: %w", strategy.Name, err)
	}

	before := strategy.Status.DeepCopy()
	r.describe(&strategy, records.Items)

	// A status write is a watch event, so a controller that writes on every
	// pass reconciles itself forever. meta.SetStatusCondition leaves
	// lastTransitionTime alone when the status has not changed, which is what
	// makes an unchanged condition compare equal here.
	if equality.Semantic.DeepEqual(before, &strategy.Status) {
		return ctrl.Result{}, nil
	}

	if err := r.Client.Status().Update(ctx, &strategy); err != nil {
		// Retried, unlike a Remediation's verdict — and that is not a
		// contradiction. The rule against retrying that one exists because a
		// second pass can hold a stale opinion about an execution that has
		// already finished, and the conflict is what refuses it. Nothing here
		// is an opinion: every field is recomputed from observed state, so a
		// conflict means the state moved and looking again is the answer.
		return ctrl.Result{}, fmt.Errorf("update the status of %s: %w", strategy.Name, err)
	}

	ready := meta.FindStatusCondition(strategy.Status.Conditions, v1alpha1.ConditionReady)
	r.Logger.Debug("strategy status updated",
		"strategy", strategy.Name,
		"ready", ready.Status,
		"reason", ready.Reason,
		"records", strategy.Status.ExecutionCount)
	return ctrl.Result{}, nil
}

// describe builds the status the strategy should have, from the strategy and
// the records it has produced. It writes nothing.
func (r *StrategyReconciler) describe(
	strategy *v1alpha1.RemediationStrategy, records []v1alpha1.Remediation,
) {
	strategy.Status.ObservedGeneration = strategy.Generation
	strategy.Status.ExecutionCount = int64(len(records))
	strategy.Status.LastExecutionTime = newestRecord(records)
	meta.SetStatusCondition(&strategy.Status.Conditions, r.readiness(strategy))
}

// readiness reports whether every action this strategy names is one this
// build can run.
//
// Two different mistakes reach the same answer, deliberately: a misspelled
// action and an action that exists but is disabled in the chart are the same
// fact from the strategy's point of view — remedik cannot run it. Which one it
// was is what the message is for, so it lists what this build does have.
func (r *StrategyReconciler) readiness(strategy *v1alpha1.RemediationStrategy) metav1.Condition {
	notReady := func(message string) metav1.Condition {
		return metav1.Condition{
			Type:               v1alpha1.ConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ReasonUnknownAction,
			Message:            message,
			ObservedGeneration: strategy.Generation,
		}
	}

	if err := r.Registry.ValidateNames(actionNames(strategy.Spec.Steps)); err != nil {
		return notReady(err.Error())
	}

	// The escalation is checked too, and it is the half worth checking most:
	// an escalation that cannot run is discovered when a remediation has
	// already failed, which is the moment with the least attention available.
	if err := r.Registry.ValidateNames(actionNames(strategy.Spec.OnFailure.Steps)); err != nil {
		return notReady(fmt.Sprintf("onFailure %s", err))
	}

	return metav1.Condition{
		Type:               v1alpha1.ConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             v1alpha1.ReasonUsable,
		Message:            "every action this strategy names is enabled in this build",
		ObservedGeneration: strategy.Generation,
	}
}

// actionNames is the plan as the registry wants to see it.
func actionNames(steps []v1alpha1.Step) []string {
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.Action)
	}
	return names
}

// newestRecord is when this strategy last produced a record, or nil if it
// never has.
//
// Creation time, not completion: the question the column answers is when this
// strategy last fired, and a record that is still running has fired.
func newestRecord(records []v1alpha1.Remediation) *metav1.Time {
	var newest *metav1.Time
	for i := range records {
		created := records[i].CreationTimestamp
		if newest == nil || created.After(newest.Time) {
			newest = created.DeepCopy()
		}
	}
	return newest
}

// SetupWithManager registers the reconciler.
func (r *StrategyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.RemediationStrategy{}).
		Named("remediationstrategy").
		// The counter is derived from the records, so the records are an
		// input. Only their appearance and disappearance change it: a record
		// moving from Running to Succeeded says nothing about how many there
		// are, and during an alert storm those updates are most of the
		// traffic, so they are dropped rather than reconciled into a no-op.
		Watches(&v1alpha1.Remediation{},
			handler.EnqueueRequestsFromMapFunc(strategyOfRecord),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: func(event.UpdateEvent) bool { return false },
			}),
		).
		Complete(r)
}

// strategyOfRecord maps a Remediation to the strategy that produced it.
//
// By the label rather than spec.strategyName: it is already on every record,
// it is what the reconciler's pruning uses, and a mapping function is handed
// an object that may be a tombstone, where labels survive and the spec is not
// worth relying on.
func strategyOfRecord(_ context.Context, obj client.Object) []reconcile.Request {
	name := obj.GetLabels()[v1alpha1.LabelStrategy]
	if name == "" {
		return nil
	}
	// Strategies are cluster-scoped: the name is the whole address.
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name}}}
}
