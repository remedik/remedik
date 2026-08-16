package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ratyx/remedik/api/v1alpha1"
	"github.com/ratyx/remedik/internal/action"
)

var testClock = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

// scriptedAction is a test double that records how it was called.
type scriptedAction struct {
	name       string
	planCalls  int
	execCalls  int
	resolveErr error
	planErr    error
	execErr    error
	target     action.Target
}

func (a *scriptedAction) Name() string { return a.name }

func (a *scriptedAction) Resolve(_ map[string]string, _ action.Params) (action.Target, error) {
	if a.resolveErr != nil {
		return action.Target{}, a.resolveErr
	}
	if a.target.IsZero() {
		return action.Target{Kind: "Deployment", Namespace: "payments", Name: "api"}, nil
	}
	return a.target, nil
}

func (a *scriptedAction) Plan(_ context.Context, t action.Target, _ action.Params) (action.Result, error) {
	a.planCalls++
	if a.planErr != nil {
		return action.Result{}, a.planErr
	}
	return action.Result{
		Summary: "would restart " + t.String(),
		Kubectl: "kubectl rollout restart " + t.String(),
	}, nil
}

func (a *scriptedAction) Execute(_ context.Context, t action.Target, _ action.Params) (action.Result, error) {
	a.execCalls++
	if a.execErr != nil {
		return action.Result{}, a.execErr
	}
	result := action.Result{
		Summary: "restarted " + t.String(),
		Kubectl: "kubectl rollout restart " + t.String(),
	}
	result.Output("resourceVersion", "42")
	return result, nil
}

// verifyingAction is a scriptedAction that also checks its own work, so the
// engine's handling of a post-condition can be tested without a cluster.
type verifyingAction struct {
	scriptedAction
	verifyCalls int
	verifyErr   error
	verifySays  string
}

func (a *verifyingAction) Verify(
	ctx context.Context, _ action.Target, _ action.Params,
) (action.Result, error) {
	a.verifyCalls++
	says := a.verifySays
	if says == "" {
		says = "3/3 replicas updated, available and ready"
	}
	if a.verifyErr != nil {
		return action.Result{Summary: says}, a.verifyErr
	}
	select {
	case <-ctx.Done():
		return action.Result{Summary: says}, ctx.Err()
	default:
	}
	return action.Result{Summary: says}, nil
}

func newRunner(t *testing.T, dryRun bool, actions ...action.Action) *StepRunner {
	t.Helper()
	reg, err := action.NewRegistry(actions...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return &StepRunner{Registry: reg, DryRun: dryRun, Now: func() time.Time { return testClock }}
}

var alertLabels = map[string]string{"alertname": "KubePodCrashLooping", "namespace": "payments"}

func TestStepRunner_AllStepsSucceed(t *testing.T) {
	first := &scriptedAction{name: "deployment.restart"}
	second := &scriptedAction{name: "deployment.scale"}
	r := newRunner(t, false, first, second)

	got := r.Run(context.Background(), alertLabels, []v1alpha1.Step{
		{Action: "deployment.restart"},
		{Action: "deployment.scale"},
	})

	if got.Err != nil {
		t.Fatalf("Err = %v, want nil", got.Err)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("recorded %d steps, want 2", len(got.Steps))
	}
	for i, s := range got.Steps {
		if s.Phase != v1alpha1.StepPhaseSucceeded {
			t.Errorf("step %d phase = %q, want Succeeded", i, s.Phase)
		}
		if s.Plan == "" {
			t.Errorf("step %d has no record of what it did", i)
		}
		if s.StartedAt == nil || s.CompletedAt == nil {
			t.Errorf("step %d is missing timestamps", i)
		}
		if s.Index != int32(i) {
			t.Errorf("step %d index = %d", i, s.Index)
		}
	}
	if state := TerminalState(false, got.Err); state != v1alpha1.RemediationStateSucceeded {
		t.Errorf("TerminalState = %q, want Succeeded", state)
	}
}

// Dry-run must reach Plan and never Execute — the guarantee that a
// Simulated remediation cannot touch the cluster.
func TestStepRunner_DryRunNeverExecutes(t *testing.T) {
	a := &scriptedAction{name: "deployment.restart"}
	r := newRunner(t, true, a)

	got := r.Run(context.Background(), alertLabels, []v1alpha1.Step{{Action: "deployment.restart"}})

	if got.Err != nil {
		t.Fatalf("Err = %v, want nil", got.Err)
	}
	if a.execCalls != 0 {
		t.Errorf("Execute was called %d times in dry-run", a.execCalls)
	}
	if a.planCalls != 1 {
		t.Errorf("Plan was called %d times, want 1", a.planCalls)
	}
	if got.Steps[0].Phase != v1alpha1.StepPhaseSimulated {
		t.Errorf("phase = %q, want Simulated", got.Steps[0].Phase)
	}
	if got.Steps[0].Plan != "would restart deployment/payments/api" {
		t.Errorf("plan = %q, want the description of what would have happened", got.Steps[0].Plan)
	}
	if state := TerminalState(true, got.Err); state != v1alpha1.RemediationStateSimulated {
		t.Errorf("TerminalState = %q, want Simulated", state)
	}
}

func TestStepRunner_StopsAtFirstFailure(t *testing.T) {
	failing := &scriptedAction{name: "deployment.restart", execErr: errors.New("deployments.apps \"api\" not found")}
	later := &scriptedAction{name: "deployment.scale"}
	r := newRunner(t, false, failing, later)

	got := r.Run(context.Background(), alertLabels, []v1alpha1.Step{
		{Action: "deployment.restart"},
		{Action: "deployment.scale"},
	})

	if got.Err == nil {
		t.Fatal("Err = nil, want the step failure")
	}
	if got.Reason != v1alpha1.ReasonStepFailed {
		t.Errorf("Reason = %q, want %q", got.Reason, v1alpha1.ReasonStepFailed)
	}
	if got.Steps[0].Phase != v1alpha1.StepPhaseFailed {
		t.Errorf("step 0 phase = %q, want Failed", got.Steps[0].Phase)
	}
	if got.Steps[0].Message == "" {
		t.Error("step 0 has no failure message")
	}
	// The step that never ran must say so, not be missing.
	if got.Steps[1].Phase != v1alpha1.StepPhaseSkipped {
		t.Errorf("step 1 phase = %q, want Skipped", got.Steps[1].Phase)
	}
	if later.execCalls != 0 {
		t.Errorf("the second action ran %d times after a failure", later.execCalls)
	}
	if state := TerminalState(false, got.Err); state != v1alpha1.RemediationStateFailed {
		t.Errorf("TerminalState = %q, want Failed", state)
	}
}

func TestStepRunner_UnknownAction(t *testing.T) {
	r := newRunner(t, false, &scriptedAction{name: "deployment.restart"})

	got := r.Run(context.Background(), alertLabels, []v1alpha1.Step{{Action: "deployment.restrt"}})

	if got.Err == nil {
		t.Fatal("Err = nil, want an unknown-action error")
	}
	if got.Reason != v1alpha1.ReasonUnknownAction {
		t.Errorf("Reason = %q, want %q", got.Reason, v1alpha1.ReasonUnknownAction)
	}
	if got.Steps[0].Phase != v1alpha1.StepPhaseFailed {
		t.Errorf("phase = %q, want Failed", got.Steps[0].Phase)
	}
}

func TestStepRunner_ResolveFailure(t *testing.T) {
	a := &scriptedAction{name: "deployment.restart", resolveErr: errors.New("alert has no namespace label")}
	r := newRunner(t, false, a)

	got := r.Run(context.Background(), alertLabels, []v1alpha1.Step{{Action: "deployment.restart"}})

	if got.Err == nil {
		t.Fatal("Err = nil, want the resolve failure")
	}
	if a.execCalls != 0 || a.planCalls != 0 {
		t.Error("the action ran despite an unresolved target")
	}
	if got.Reason != v1alpha1.ReasonStepFailed {
		t.Errorf("Reason = %q, want %q", got.Reason, v1alpha1.ReasonStepFailed)
	}
}

func TestStepRunner_DryRunPlanFailure(t *testing.T) {
	a := &scriptedAction{name: "deployment.restart", planErr: errors.New("deployment not found")}
	r := newRunner(t, true, a)

	got := r.Run(context.Background(), alertLabels, []v1alpha1.Step{{Action: "deployment.restart"}})

	if got.Err == nil {
		t.Fatal("Err = nil, want the plan failure")
	}
	if a.execCalls != 0 {
		t.Error("Execute was called after a failed Plan in dry-run")
	}
	if got.Steps[0].Phase != v1alpha1.StepPhaseFailed {
		t.Errorf("phase = %q, want Failed", got.Steps[0].Phase)
	}
}

func TestStepRunner_EmptyPlan(t *testing.T) {
	r := newRunner(t, false, &scriptedAction{name: "deployment.restart"})

	got := r.Run(context.Background(), alertLabels, nil)

	if got.Err != nil {
		t.Errorf("Err = %v, want nil", got.Err)
	}
	if len(got.Steps) != 0 {
		t.Errorf("recorded %d steps for an empty plan", len(got.Steps))
	}
}

// errUnresolvable is shared by the sink tests.
var errUnresolvable = errors.New("alert carries no deployment label")
