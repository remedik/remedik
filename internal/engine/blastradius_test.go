package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/alert"
	"github.com/remedik/remedik/internal/guards"
)

// fixedWorkload answers the guard the same way every time.
type fixedWorkload struct {
	workload   guards.Workload
	applicable bool
	err        error
	calls      int
}

func (f *fixedWorkload) Workload(context.Context, string) (guards.Workload, bool, error) {
	f.calls++
	return f.workload, f.applicable, f.err
}

// withBlastRadius returns a strategy option setting the guard.
func withBlastRadius(minAvailable, maxUnavailablePercent int32) func(*v1alpha1.RemediationStrategy) {
	return func(s *v1alpha1.RemediationStrategy) {
		s.Spec.Guards.BlastRadius = &v1alpha1.BlastRadius{
			MinAvailable:          minAvailable,
			MaxUnavailablePercent: maxUnavailablePercent,
		}
	}
}

func TestSink_BlastRadiusRefusesADegradedWorkload(t *testing.T) {
	f := newSink(t, false, strategy("restart-api",
		map[string]string{"alertname": "KubePodCrashLooping"},
		withBlastRadius(1, 0)))
	f.sink.Workloads = &fixedWorkload{
		workload:   guards.Workload{Name: "deployment/payments/api", Desired: 3, Available: 1},
		applicable: true,
	}
	events := &recordingEvents{}
	f.sink.Events = events

	f.sink.Consume([]alert.Alert{firingAlert()})

	if got := len(f.client.remediations()); got != 0 {
		t.Fatalf("created %d remediations, want 0: the workload has one replica left", got)
	}
	// The refusal has to reach the strategy, or "why did nothing happen?"
	// has no answer where people look.
	if len(events.events) != 1 || !strings.Contains(events.events[0], "blastRadius") {
		t.Fatalf("events = %v, want one naming blastRadius", events.events)
	}
	if !strings.Contains(events.events[0], "deployment/payments/api") {
		t.Errorf("event = %q, want it to name the workload", events.events[0])
	}
}

func TestSink_BlastRadiusAllowsAHealthyWorkload(t *testing.T) {
	f := newSink(t, false, strategy("restart-api",
		map[string]string{"alertname": "KubePodCrashLooping"},
		withBlastRadius(1, 25)))
	f.sink.Workloads = &fixedWorkload{
		workload:   guards.Workload{Name: "deployment/payments/api", Desired: 3, Available: 3},
		applicable: true,
	}

	f.sink.Consume([]alert.Alert{firingAlert()})

	if got := len(f.client.remediations()); got != 1 {
		t.Fatalf("created %d remediations, want 1", got)
	}
}

// A guard that permits an execution when it could not evaluate its own
// condition is not a guard.
func TestSink_BlastRadiusFailsClosed(t *testing.T) {
	f := newSink(t, false, strategy("restart-api",
		map[string]string{"alertname": "KubePodCrashLooping"},
		withBlastRadius(1, 0)))
	f.sink.Workloads = &fixedWorkload{err: errors.New("deployments.apps is forbidden")}
	events := &recordingEvents{}
	f.sink.Events = events

	f.sink.Consume([]alert.Alert{firingAlert()})

	if got := len(f.client.remediations()); got != 0 {
		t.Fatalf("created %d remediations despite being unable to evaluate the guard", got)
	}
	if len(events.events) != 1 || !strings.Contains(events.events[0], "guards.blastRadius.enabled") {
		t.Fatalf("events = %v, want one naming the permission to grant", events.events)
	}
}

// The guard is opt-in like every other: an unset block costs nothing,
// including the read.
func TestSink_BlastRadiusUnsetIsNotEvaluated(t *testing.T) {
	f := newSink(t, false, strategy("restart-api",
		map[string]string{"alertname": "KubePodCrashLooping"}))
	reader := &fixedWorkload{err: errors.New("would refuse if asked")}
	f.sink.Workloads = reader

	f.sink.Consume([]alert.Alert{firingAlert()})

	if got := len(f.client.remediations()); got != 1 {
		t.Fatalf("created %d remediations, want 1: an unset guard must not refuse", got)
	}
	if reader.calls != 0 {
		t.Errorf("read the workload %d times for a strategy that configures no blastRadius", reader.calls)
	}
}

// The cheap guards answer first: a cooldown refusal must not cost a read.
func TestSink_CooldownIsCheckedBeforeBlastRadius(t *testing.T) {
	f := newSink(t, false, strategy("restart-api",
		map[string]string{"alertname": "KubePodCrashLooping"},
		func(s *v1alpha1.RemediationStrategy) {
			s.Spec.Guards.Cooldown = &metav1.Duration{Duration: 15 * 60 * 1e9}
		},
		withBlastRadius(1, 0)))
	reader := &fixedWorkload{
		workload:   guards.Workload{Name: "deployment/payments/api", Desired: 3, Available: 3},
		applicable: true,
	}
	f.sink.Workloads = reader
	f.history.RecordCompletion("restart-api", "deployment/payments/api", testClock.Add(-1*60*1e9))

	f.sink.Consume([]alert.Alert{firingAlert()})

	if got := len(f.client.remediations()); got != 0 {
		t.Fatalf("created %d remediations inside the cooldown", got)
	}
	if reader.calls != 0 {
		t.Errorf("read the cluster %d times for an alert the cooldown had already refused", reader.calls)
	}
}
