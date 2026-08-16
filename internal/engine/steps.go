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
	// Events publishes what a step is doing onto the object it is doing it
	// to. Optional: nil publishes nothing, which is what the unit tests
	// want and what a cluster without an event recorder gets.
	Events StepEvents
	// Now supplies timestamps; tests inject a fixed clock.
	Now func() time.Time
}

// StepEvents announces a step's progress on the object being remediated.
//
// It exists so that `kubectl describe deployment payments/api` explains an
// unexpected restart, without the reader having to already know remedik
// exists and go looking for its records. Publishing is best-effort by
// contract: an event that cannot be sent is never a reason to fail a
// remediation that otherwise worked, so no method returns an error.
type StepEvents interface {
	// Starting announces that a step is about to run.
	Starting(ctx context.Context, target action.Target, actionName string, index int)
	// Succeeded announces that a step completed, with its summary.
	Succeeded(ctx context.Context, target action.Target, actionName string, index int, summary string)
	// Finished announces that a step ended in failure.
	Finished(ctx context.Context, target action.Target, actionName string, index int, err error)
}

// NopStepEvents publishes nothing.
type NopStepEvents struct{}

// Starting implements StepEvents.
func (NopStepEvents) Starting(context.Context, action.Target, string, int) {}

// Succeeded implements StepEvents.
func (NopStepEvents) Succeeded(context.Context, action.Target, string, int, string) {}

// Finished implements StepEvents.
func (NopStepEvents) Finished(context.Context, action.Target, string, int, error) {}

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

	// record folds an action's Result onto the step. Everything the action
	// chose to say is kept: the summary leads, the kubectl equivalent makes
	// it reviewable, and the outputs stay machine-readable.
	record := func(result action.Result) {
		status.Plan = result.Summary
		if result.Kubectl != "" {
			status.Kubectl = result.Kubectl
		}
		for key, value := range result.Outputs {
			if status.Outputs == nil {
				status.Outputs = make(map[string]string, len(result.Outputs))
			}
			status.Outputs[key] = value
		}
	}

	finish := func(phase v1alpha1.StepPhase, message string) v1alpha1.StepStatus {
		completed := metav1.NewTime(now())
		status.Phase = phase
		status.Message = message
		status.CompletedAt = &completed
		return status
	}

	fail := func(err error) (v1alpha1.StepStatus, error) {
		return finish(v1alpha1.StepPhaseFailed, err.Error()), err
	}

	act, err := r.Registry.Get(step.Action)
	if err != nil {
		return fail(err)
	}

	params := action.Params(step.With)

	target, err := act.Resolve(labels, params)
	if err != nil {
		return fail(fmt.Errorf("resolve target for %s: %w", step.Action, err))
	}
	status.Target = target.String()

	if r.DryRun {
		planned, planErr := act.Plan(ctx, target, params)
		record(planned)
		if planErr != nil {
			return fail(fmt.Errorf("plan %s on %s: %w", step.Action, target, planErr))
		}
		return finish(v1alpha1.StepPhaseSimulated, ""), nil
	}

	// Announced before the work starts, not after: the event that matters
	// most is the one explaining a change while it is happening, which is
	// exactly the minute somebody is looking at the workload wondering what
	// is restarting it.
	events := r.events()
	events.Starting(ctx, target, step.Action, index)

	done, execErr := act.Execute(ctx, target, params)
	record(done)
	if execErr != nil {
		err := fmt.Errorf("execute %s on %s: %w", step.Action, target, execErr)
		events.Finished(ctx, target, step.Action, index, err)
		return fail(err)
	}

	// The action changed something; whether that fixed anything is a
	// separate question, and one only the action can answer.
	if err := r.verify(ctx, act, target, params, done, &status); err != nil {
		events.Finished(ctx, target, step.Action, index, err)
		return fail(err)
	}

	events.Succeeded(ctx, target, step.Action, index, status.Plan)
	return finish(v1alpha1.StepPhaseSucceeded, ""), nil
}

// verify runs an action's post-condition check, if it has one.
//
// A step whose check fails is a failed step. The alternative — succeeding
// while recording that verification did not — would add a third outcome the
// state machine does not have and an operator would have to learn, when the
// honest reading is simply that the remediation did not work.
func (r *StepRunner) verify(
	ctx context.Context,
	act action.Action,
	target action.Target,
	params action.Params,
	executed action.Result,
	status *v1alpha1.StepStatus,
) error {
	verifier, ok := act.(action.Verifier)
	if !ok {
		return nil
	}

	timeout, err := action.VerifyTimeout(params)
	if err != nil {
		return fmt.Errorf("verify %s on %s: %w", act.Name(), target, err)
	}

	verifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := verifier.Verify(verifyCtx, target, params, executed)
	// The summary is recorded either way: what the check saw is the most
	// useful thing on the page when it failed, not only when it passed.
	if result.Summary != "" {
		status.Verified = result.Summary
	}
	for key, value := range result.Outputs {
		if status.Outputs == nil {
			status.Outputs = make(map[string]string, len(result.Outputs))
		}
		status.Outputs[key] = value
	}
	if err != nil {
		return fmt.Errorf("%s ran on %s but did not take effect: %w", act.Name(), target, err)
	}
	return nil
}

func (r *StepRunner) events() StepEvents {
	if r.Events == nil {
		return NopStepEvents{}
	}
	return r.Events
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
