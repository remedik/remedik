package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/action"
)

// recordedEvent is one publication, captured so a test can assert on what
// an operator would see with `kubectl describe`.
type recordedEvent struct {
	target string
	action string
	index  int
	kind   string // "starting", "succeeded" or "failed"
	detail string
}

// recordingStepEvents captures step events instead of sending them anywhere.
type recordingStepEvents struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (e *recordingStepEvents) Starting(_ context.Context, t action.Target, name string, index int) {
	e.add(recordedEvent{target: t.String(), action: name, index: index, kind: "starting"})
}

func (e *recordingStepEvents) Succeeded(_ context.Context, t action.Target, name string, index int, summary string) {
	e.add(recordedEvent{target: t.String(), action: name, index: index, kind: "succeeded", detail: summary})
}

func (e *recordingStepEvents) Finished(_ context.Context, t action.Target, name string, index int, err error) {
	if err == nil {
		return
	}
	e.add(recordedEvent{target: t.String(), action: name, index: index, kind: "failed", detail: err.Error()})
}

func (e *recordingStepEvents) add(ev recordedEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
}

func (e *recordingStepEvents) kinds() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.events))
	for _, ev := range e.events {
		out = append(out, ev.kind)
	}
	return out
}

func plan(actions ...string) []v1alpha1.Step {
	steps := make([]v1alpha1.Step, 0, len(actions))
	for _, name := range actions {
		steps = append(steps, v1alpha1.Step{Action: name})
	}
	return steps
}

// --------------------------------------------------------------------------
// The step record
// --------------------------------------------------------------------------

func TestStepRunner_RecordsWhatTheActionReported(t *testing.T) {
	a := &scriptedAction{name: "deployment.restart"}
	runner := newRunner(t, false, a)

	result := runner.Run(context.Background(), nil, plan("deployment.restart"))

	step := result.Steps[0]
	if step.Kubectl == "" {
		t.Error("the step records no kubectl equivalent, so the change is not reviewable " +
			"by anyone who has not read remedik's source")
	}
	if got := step.Outputs["resourceVersion"]; got != "42" {
		t.Errorf("outputs[resourceVersion] = %q, want 42", got)
	}
	if step.Target != "deployment/payments/api" {
		t.Errorf("step target = %q, want the object it acted on", step.Target)
	}
}

func TestStepRunner_RecordsTheTargetOfEveryStep(t *testing.T) {
	first := &scriptedAction{name: "first", target: action.Target{Kind: "Deployment", Namespace: "payments", Name: "api"}}
	second := &scriptedAction{name: "second", target: action.Target{Kind: "Node", Name: "node-3"}}
	runner := newRunner(t, false, first, second)

	result := runner.Run(context.Background(), nil, plan("first", "second"))

	// A plan may touch several objects. The target on the spec names only
	// the one the guards are scoped by, so each step has to say its own.
	if result.Steps[0].Target != "deployment/payments/api" {
		t.Errorf("step 1 target = %q", result.Steps[0].Target)
	}
	if result.Steps[1].Target != "node/node-3" {
		t.Errorf("step 2 target = %q", result.Steps[1].Target)
	}
}

// --------------------------------------------------------------------------
// Verification
// --------------------------------------------------------------------------

func TestStepRunner_VerifiesAfterExecuting(t *testing.T) {
	a := &verifyingAction{scriptedAction: scriptedAction{name: "deployment.restart"}}
	runner := newRunner(t, false, a)

	result := runner.Run(context.Background(), nil, plan("deployment.restart"))

	if result.Err != nil {
		t.Fatalf("Run() error = %v, want nil", result.Err)
	}
	if a.verifyCalls != 1 {
		t.Errorf("Verify called %d times, want 1", a.verifyCalls)
	}
	if got := result.Steps[0].Verified; !strings.Contains(got, "ready") {
		t.Errorf("verified = %q, want what the check found", got)
	}
}

func TestStepRunner_AFailedVerifyFailsTheStep(t *testing.T) {
	a := &verifyingAction{
		scriptedAction: scriptedAction{name: "deployment.restart"},
		verifyErr:      errors.New("the rollout did not complete in time"),
		verifySays:     "1/3 replicas available",
	}
	runner := newRunner(t, false, a)

	result := runner.Run(context.Background(), nil, plan("deployment.restart"))

	if result.Err == nil {
		t.Fatal("Run() error = nil; an action that ran but did not take effect is not a success")
	}
	if result.Reason != v1alpha1.ReasonStepFailed {
		t.Errorf("reason = %q, want %q", result.Reason, v1alpha1.ReasonStepFailed)
	}

	step := result.Steps[0]
	if step.Phase != v1alpha1.StepPhaseFailed {
		t.Errorf("phase = %q, want Failed", step.Phase)
	}
	// What the check saw matters most exactly when it failed.
	if step.Verified != "1/3 replicas available" {
		t.Errorf("verified = %q, want the state the check observed", step.Verified)
	}
	if !strings.Contains(step.Message, "did not take effect") {
		t.Errorf("message = %q, want it to say the action ran without taking effect", step.Message)
	}
	// The action did run, so what it did is still on the record.
	if step.Plan == "" {
		t.Error("the step lost the summary of what was executed")
	}
}

func TestStepRunner_DryRunNeverVerifies(t *testing.T) {
	a := &verifyingAction{scriptedAction: scriptedAction{name: "deployment.restart"}}
	runner := newRunner(t, true, a)

	result := runner.Run(context.Background(), nil, plan("deployment.restart"))

	if a.verifyCalls != 0 {
		t.Errorf("Verify ran %d times in dry-run; there was nothing executed to verify", a.verifyCalls)
	}
	if got := result.Steps[0].Verified; got != "" {
		t.Errorf("verified = %q in dry-run, want empty", got)
	}
	if result.Steps[0].Phase != v1alpha1.StepPhaseSimulated {
		t.Errorf("phase = %q, want Simulated", result.Steps[0].Phase)
	}
}

func TestStepRunner_AnUnverifiableActionIsStillFine(t *testing.T) {
	// scriptedAction does not implement Verifier. Nothing should require it
	// to: an action with nothing to check should say so by not implementing
	// the interface.
	a := &scriptedAction{name: "node.cordon"}
	runner := newRunner(t, false, a)

	result := runner.Run(context.Background(), nil, plan("node.cordon"))

	if result.Err != nil {
		t.Fatalf("Run() error = %v, want nil", result.Err)
	}
	if got := result.Steps[0].Verified; got != "" {
		t.Errorf("verified = %q, want empty for an action that does not check itself", got)
	}
}

func TestStepRunner_RejectsAnUnusableVerifyTimeout(t *testing.T) {
	a := &verifyingAction{scriptedAction: scriptedAction{name: "deployment.restart"}}
	runner := newRunner(t, false, a)

	steps := []v1alpha1.Step{{
		Action: "deployment.restart",
		With:   map[string]string{action.VerifyTimeoutParam: "30"}, // no unit
	}}
	result := runner.Run(context.Background(), nil, steps)

	if result.Err == nil {
		t.Fatal("Run() error = nil; a malformed timeout must not silently become the default")
	}
	if !strings.Contains(result.Steps[0].Message, action.VerifyTimeoutParam) {
		t.Errorf("message = %q, want it to name the parameter at fault", result.Steps[0].Message)
	}
}

// --------------------------------------------------------------------------
// Events on the remediated object
// --------------------------------------------------------------------------

func TestStepRunner_AnnouncesOnTheRemediatedObject(t *testing.T) {
	events := &recordingStepEvents{}
	a := &scriptedAction{name: "deployment.restart"}
	runner := newRunner(t, false, a)
	runner.Events = events

	runner.Run(context.Background(), nil, plan("deployment.restart"))

	// Starting comes first, deliberately: the event that matters most is the
	// one explaining a change while it is happening.
	want := []string{"starting", "succeeded"}
	got := events.kinds()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
	if events.events[0].target != "deployment/payments/api" {
		t.Errorf("event target = %q, want the object being remediated", events.events[0].target)
	}
}

func TestStepRunner_AnnouncesAFailure(t *testing.T) {
	events := &recordingStepEvents{}
	a := &scriptedAction{name: "deployment.restart", execErr: errors.New("conflict")}
	runner := newRunner(t, false, a)
	runner.Events = events

	runner.Run(context.Background(), nil, plan("deployment.restart"))

	got := events.kinds()
	if len(got) != 2 || got[1] != "failed" {
		t.Fatalf("events = %v, want a starting and a failed event", got)
	}
	if !strings.Contains(events.events[1].detail, "conflict") {
		t.Errorf("failure event = %q, want the action's error", events.events[1].detail)
	}
}

func TestStepRunner_DryRunAnnouncesNothing(t *testing.T) {
	events := &recordingStepEvents{}
	runner := newRunner(t, true, &scriptedAction{name: "deployment.restart"})
	runner.Events = events

	runner.Run(context.Background(), nil, plan("deployment.restart"))

	// Nothing changed, so nothing should appear on the object as though
	// something had.
	if got := events.kinds(); len(got) != 0 {
		t.Errorf("events = %v in dry-run, want none", got)
	}
}
