// Package engine turns a matched alert into an audited remediation.
//
// It is split so that the decisions live apart from the plumbing:
// StepRunner sequences a strategy's steps and needs no Kubernetes client,
// Backoff decides retry timing, and the reconciler in this package is thin
// glue over both. What can be reasoned about without a cluster is tested
// without one.
package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ratyx/remedik/api/v1alpha1"
	"github.com/ratyx/remedik/internal/action"
)

// StepRunner executes a strategy's steps in order.
//
// It is the heart of an execution and is deliberately free of any
// Kubernetes client: it takes the alert's labels and the plan, and returns
// what happened. That keeps the sequencing rules — stop at the first
// failure, skip the rest, never mutate in dry-run — testable without a
// cluster.
type StepRunner struct {
	// Registry resolves action names to implementations.
	Registry *action.Registry
	// DryRun selects Plan instead of Execute. The mutating path is not
	// merely skipped by a flag inside the action: it is never called.
	DryRun bool
	// Now supplies timestamps; tests inject a fixed clock.
	Now func() time.Time
}

// RunResult is the outcome of one attempt over a plan.
type RunResult struct {
	// Steps records every step, including those skipped after a failure.
	Steps []v1alpha1.StepStatus
	// Err is nil when every step succeeded (or was simulated).
	Err error
	// Reason is the machine-readable cause when Err is set:
	// ReasonStepFailed or ReasonUnknownAction.
	Reason string
}

// Run executes the plan and returns the per-step outcome.
//
// Execution stops at the first failed step; remaining steps are recorded as
// Skipped so the audit trail shows what did not happen as well as what did.
func (r *StepRunner) Run(ctx context.Context, labels map[string]string, plan []v1alpha1.Step) RunResult {
	now := r.now
	if r.Now != nil {
		now = r.Now
	}

	result := RunResult{Steps: make([]v1alpha1.StepStatus, 0, len(plan))}

	for i, step := range plan {
		// Once something failed, the rest never ran: say so explicitly
		// rather than leaving them absent from the record.
		if result.Err != nil {
			result.Steps = append(result.Steps, v1alpha1.StepStatus{
				Index:  int32(i), //nolint:gosec // plan length is bounded by the CRD
				Action: step.Action,
				Phase:  v1alpha1.StepPhaseSkipped,
			})
			continue
		}

		status, err := r.runStep(ctx, i, labels, step, now)
		result.Steps = append(result.Steps, status)

		if err != nil {
			result.Err = err
			result.Reason = v1alpha1.ReasonStepFailed
			if errors.Is(err, action.ErrUnknownAction) {
				result.Reason = v1alpha1.ReasonUnknownAction
			}
		}
	}

	return result
}

func (r *StepRunner) runStep(
	ctx context.Context,
	index int,
	labels map[string]string,
	step v1alpha1.Step,
	now func() time.Time,
) (v1alpha1.StepStatus, error) {
	started := metav1.NewTime(now())
	status := v1alpha1.StepStatus{
		Index:     int32(index), //nolint:gosec // plan length is bounded by the CRD
		Action:    step.Action,
		Phase:     v1alpha1.StepPhaseRunning,
		StartedAt: &started,
	}

	finish := func(phase v1alpha1.StepPhase, plan, message string) v1alpha1.StepStatus {
		completed := metav1.NewTime(now())
		status.Phase = phase
		status.Plan = plan
		status.Message = message
		status.CompletedAt = &completed
		return status
	}

	act, err := r.Registry.Get(step.Action)
	if err != nil {
		return finish(v1alpha1.StepPhaseFailed, "", err.Error()), err
	}

	params := action.Params(step.With)

	target, err := act.Resolve(labels, params)
	if err != nil {
		err = fmt.Errorf("resolve target for %s: %w", step.Action, err)
		return finish(v1alpha1.StepPhaseFailed, "", err.Error()), err
	}

	if r.DryRun {
		plan, planErr := act.Plan(ctx, target, params)
		if planErr != nil {
			planErr = fmt.Errorf("plan %s on %s: %w", step.Action, target, planErr)
			return finish(v1alpha1.StepPhaseFailed, "", planErr.Error()), planErr
		}
		return finish(v1alpha1.StepPhaseSimulated, plan, ""), nil
	}

	done, execErr := act.Execute(ctx, target, params)
	if execErr != nil {
		execErr = fmt.Errorf("execute %s on %s: %w", step.Action, target, execErr)
		return finish(v1alpha1.StepPhaseFailed, "", execErr.Error()), execErr
	}
	return finish(v1alpha1.StepPhaseSucceeded, done, ""), nil
}

func (r *StepRunner) now() time.Time { return time.Now() }

// TerminalState maps a run outcome to the state recorded on the
// Remediation. Dry-run always ends Simulated, even on success, so that a
// record can never be mistaken for something that touched the cluster.
func TerminalState(dryRun bool, err error) v1alpha1.RemediationState {
	switch {
	case err != nil:
		return v1alpha1.RemediationStateFailed
	case dryRun:
		return v1alpha1.RemediationStateSimulated
	default:
		return v1alpha1.RemediationStateSucceeded
	}
}
