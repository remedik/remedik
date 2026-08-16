package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/api/v1alpha1"
	"github.com/ratyx/remedik/internal/action"
	"github.com/ratyx/remedik/internal/guards"
)

// DefaultHistoryLimit is how many terminal Remediation resources are kept
// per strategy before the oldest are pruned.
const DefaultHistoryLimit = 200

// RemediationReconciler drives one Remediation through its lifecycle.
//
// The state machine is deliberately shaped so that crash recovery needs no
// bookkeeping. An attempt runs to completion inside a single Reconcile
// call, so a Remediation found in Running can only mean one thing: the
// process died mid-execution. It is failed as Interrupted rather than
// resumed, because resuming a mutating step safely would require
// per-action idempotency guarantees that do not exist yet, and silently
// repeating an action is the worse outcome.
//
//	(new) --> Pending --> Running --> Succeeded | Simulated
//	             ^                |
//	             |                +--> Failed        (no retries left)
//	             +----------------+--> Pending       (retry, after backoff)
//	          Running on entry ------> Failed/Interrupted
//
// Waiting for a retry is Pending, never Running: that keeps "Running means
// interrupted" true, and it means the operator is not holding a worker
// while it waits.
type RemediationReconciler struct {
	// Client reads and writes Remediation resources.
	Client client.Client
	// Registry resolves step actions.
	Registry *action.Registry
	// History is updated as executions complete, so guards see them.
	History *guards.MemoryHistory
	// DryRun selects the Plan-only path.
	DryRun bool
	// HistoryLimit caps terminal records kept per strategy.
	HistoryLimit int
	// Metrics receives telemetry; defaults to NopRecorder.
	Metrics Recorder
	// Events publishes step progress on the objects being remediated, so
	// that `kubectl describe` explains a change where the reader is already
	// looking. Optional: nil publishes nothing.
	Events events.EventRecorder
	// Mapper addresses a step's target for those events. Optional; without
	// it no event can be addressed and none are published.
	Mapper meta.RESTMapper
	// Logger is required.
	Logger *slog.Logger
	// Now supplies timestamps; tests inject a fixed clock.
	Now func() time.Time
}

// Reconcile implements reconcile.Reconciler.
func (r *RemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var rem v1alpha1.Remediation
	if err := r.Client.Get(ctx, req.NamespacedName, &rem); err != nil {
		// A deleted Remediation is not an error: pruning removes them.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log := r.Logger.With(
		"remediation", rem.Name,
		"strategy", rem.Spec.StrategyName,
		"target", rem.Spec.Target)

	switch {
	case rem.Status.State.IsTerminal():
		return ctrl.Result{}, nil

	case rem.Status.State == v1alpha1.RemediationStateRunning:
		return ctrl.Result{}, r.markInterrupted(ctx, &rem, log)

	default:
		return r.runAttempt(ctx, &rem, log)
	}
}

// markInterrupted fails an execution the operator did not finish.
func (r *RemediationReconciler) markInterrupted(
	ctx context.Context, rem *v1alpha1.Remediation, log *slog.Logger,
) error {
	log.Warn("remediation was interrupted; the operator restarted mid-execution",
		"attempt", rem.Status.Attempt)

	// Steps left mid-flight are not reported as still running.
	for i := range rem.Status.Steps {
		if rem.Status.Steps[i].Phase == v1alpha1.StepPhaseRunning {
			rem.Status.Steps[i].Phase = v1alpha1.StepPhaseFailed
			rem.Status.Steps[i].Message = "the operator restarted while this step was running"
		}
	}

	return r.finish(ctx, rem, v1alpha1.RemediationStateFailed,
		v1alpha1.ReasonInterrupted,
		"the operator restarted while this remediation was running", log)
}

// runAttempt executes every step once and records the outcome.
func (r *RemediationReconciler) runAttempt(
	ctx context.Context, rem *v1alpha1.Remediation, log *slog.Logger,
) (ctrl.Result, error) {
	attempt := rem.Status.Attempt + 1
	log = log.With("attempt", attempt)

	// Persist Running before doing anything: this write is what makes an
	// interrupted execution detectable.
	started := metav1.NewTime(r.now())
	rem.Status.State = v1alpha1.RemediationStateRunning
	rem.Status.Attempt = attempt
	rem.Status.Steps = nil
	if rem.Status.StartedAt == nil {
		rem.Status.StartedAt = &started
	}
	if err := r.Client.Status().Update(ctx, rem); err != nil {
		return ctrl.Result{}, fmt.Errorf("mark remediation running: %w", err)
	}

	runner := &StepRunner{
		Registry:    r.Registry,
		Remediation: rem.Name,
		Strategy:    rem.Spec.StrategyName,
		Namespace:   rem.Namespace,
		DryRun:      rem.Spec.DryRun || r.DryRun,
		Events:      r.stepEvents(rem),
		Now:         r.Now,
	}
	result := runner.Run(ctx, rem.Spec.Alert.Labels, rem.Spec.Steps)
	rem.Status.Steps = result.Steps

	if result.Err == nil {
		state := TerminalState(runner.DryRun, nil)
		log.Info("remediation finished", "state", state)
		return ctrl.Result{}, r.finish(ctx, rem, state, "", "", log)
	}

	// Failed. Retry if the strategy allows it.
	if retries := retriesFor(rem); attempt <= retries {
		wait := Backoff(attempt + 1)
		log.Warn("attempt failed; retrying",
			"err", result.Err, "retry_in", wait, "retries_left", retries-attempt+1)

		rem.Status.State = v1alpha1.RemediationStatePending
		rem.Status.Reason = result.Reason
		rem.Status.Message = result.Err.Error()
		if err := r.Client.Status().Update(ctx, rem); err != nil {
			return ctrl.Result{}, fmt.Errorf("record failed attempt: %w", err)
		}
		return ctrl.Result{RequeueAfter: wait}, nil
	}

	log.Error("remediation failed", "err", result.Err, "reason", result.Reason)
	return ctrl.Result{}, r.finish(ctx, rem,
		v1alpha1.RemediationStateFailed, result.Reason, result.Err.Error(), log)
}

// finish writes the terminal state, updates guard history and prunes.
func (r *RemediationReconciler) finish(
	ctx context.Context,
	rem *v1alpha1.Remediation,
	state v1alpha1.RemediationState,
	reason, message string,
	log *slog.Logger,
) error {
	completed := metav1.NewTime(r.now())
	rem.Status.State = state
	rem.Status.Reason = reason
	rem.Status.Message = message
	rem.Status.CompletedAt = &completed

	if err := r.Client.Status().Update(ctx, rem); err != nil {
		return fmt.Errorf("record terminal state: %w", err)
	}

	// The cooldown guard is scoped by (strategy, target) and applies
	// however the execution ended: a failed remediation that is retried
	// immediately by the next alert is exactly the loop cooldown exists to
	// prevent.
	if rem.Spec.Target != "" {
		r.History.RecordCompletion(rem.Spec.StrategyName, rem.Spec.Target, completed.Time)
	}

	seconds := 0.0
	if rem.Status.StartedAt != nil {
		seconds = completed.Sub(rem.Status.StartedAt.Time).Seconds()
	}
	r.metrics().RemediationFinished(rem.Spec.StrategyName, string(state), seconds)

	if err := r.prune(ctx, rem.Namespace, rem.Spec.StrategyName, rem.Name); err != nil {
		// Pruning is housekeeping: a failure here must not turn a
		// completed remediation into a reconcile error and a requeue.
		log.Warn("could not prune old remediations", "err", err)
	}
	return nil
}

// prune keeps the newest terminal records per strategy and deletes the
// rest, so an alert storm cannot grow etcd without bound.
//
// The record that just completed is never a candidate, whatever the
// timestamps say. Deleting it would destroy the entry an operator is most
// likely to go looking for, and relying on clock ordering to protect the
// newest record is the kind of assumption that fails under skew.
func (r *RemediationReconciler) prune(ctx context.Context, namespace, strategy, keep string) error {
	limit := r.HistoryLimit
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}

	var list v1alpha1.RemediationList
	if err := r.Client.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{v1alpha1.LabelStrategy: strategy},
	); err != nil {
		return err
	}

	terminal := make([]*v1alpha1.Remediation, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].Status.State.IsTerminal() && list.Items[i].Name != keep {
			terminal = append(terminal, &list.Items[i])
		}
	}
	// The kept record counts against the limit even though it is excluded
	// from the candidates.
	if len(terminal)+1 <= limit {
		return nil
	}
	budget := limit - 1
	if budget < 0 {
		budget = 0
	}

	// Newest first, so the tail is what gets deleted. Creation timestamp
	// has second granularity; the name breaks ties so the order is total
	// and pruning cannot oscillate.
	sort.Slice(terminal, func(i, j int) bool {
		ti, tj := terminal[i].CreationTimestamp.Time, terminal[j].CreationTimestamp.Time
		if ti.Equal(tj) {
			return terminal[i].Name > terminal[j].Name
		}
		return ti.After(tj)
	})

	for _, old := range terminal[budget:] {
		if err := r.Client.Delete(ctx, old); err != nil && !isNotFound(err) {
			return fmt.Errorf("delete %s: %w", old.Name, err)
		}
	}
	return nil
}

// SetupWithManager registers the reconciler.
func (r *RemediationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Remediation{}).
		Named("remediation").
		Complete(r)
}

func (r *RemediationReconciler) metrics() Recorder {
	if r.Metrics == nil {
		return NopRecorder{}
	}
	return r.Metrics
}

// stepEvents builds the publisher for one execution, so that every event it
// sends names the remediation and the strategy that caused it — the two
// things a reader needs in order to get from "why did this restart?" to the
// record and the manifest that decided it.
func (r *RemediationReconciler) stepEvents(rem *v1alpha1.Remediation) StepEvents {
	if r.Events == nil || r.Mapper == nil {
		return NopStepEvents{}
	}
	return &TargetEvents{
		Recorder:    r.Events,
		Mapper:      r.Mapper,
		Remediation: rem.Name,
		Strategy:    rem.Spec.StrategyName,
		Namespace:   rem.Namespace,
		Logger:      r.Logger,
	}
}

func (r *RemediationReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func retriesFor(rem *v1alpha1.Remediation) int32 {
	// The plan is copied onto the Remediation at creation, but the retry
	// budget lives on the strategy, which may since have changed. Reading
	// it from the record keeps an in-flight execution stable; it is stored
	// in the annotation written at creation time.
	return rem.Spec.Retries
}

func isNotFound(err error) bool {
	return client.IgnoreNotFound(err) == nil
}
