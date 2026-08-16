package engine

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/guards"
)

func finished(name, target string, started, completed time.Time) *v1alpha1.Remediation {
	rem := remediation(name)
	rem.Spec.Target = target
	rem.CreationTimestamp = metav1.NewTime(started)
	rem.Status = v1alpha1.RemediationStatus{
		State:       v1alpha1.RemediationStateSucceeded,
		StartedAt:   &metav1.Time{Time: started},
		CompletedAt: &metav1.Time{Time: completed},
	}
	return rem
}

func newLoader(objs ...client.Object) (*HistoryLoader, *guards.MemoryHistory) {
	history := guards.NewMemoryHistory(0)
	return &HistoryLoader{
		Reader:    newFakeClient(objs...),
		History:   history,
		Namespace: testNamespace,
		Logger:    quietLogger(),
	}, history
}

// The scenario this exists for: the operator restarts, and the cooldown
// that was in force must still be in force.
func TestHistoryLoader_CooldownSurvivesARestart(t *testing.T) {
	now := time.Now()
	loader, history := newLoader(
		finished("rem-1", "deployment/payments/api", now.Add(-10*time.Minute), now.Add(-9*time.Minute)),
	)

	if err := loader.Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	decision := guards.Evaluate(
		guards.Config{Cooldown: 30 * time.Minute}, history,
		"restart-api", "deployment/payments/api", now)

	if decision.Allowed {
		t.Error("the cooldown was forgotten across the restart")
	}
	if decision.Guard != guards.GuardCooldown {
		t.Errorf("Guard = %q, want %q", decision.Guard, guards.GuardCooldown)
	}
}

func TestHistoryLoader_RateLimitSurvivesARestart(t *testing.T) {
	now := time.Now()
	loader, history := newLoader(
		finished("rem-1", "deployment/payments/api", now.Add(-30*time.Minute), now.Add(-29*time.Minute)),
		finished("rem-2", "deployment/payments/web", now.Add(-20*time.Minute), now.Add(-19*time.Minute)),
	)

	if err := loader.Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if got := history.StartsSince("restart-api", now.Add(-time.Hour)); got != 2 {
		t.Errorf("StartsSince() = %d, want 2 replayed starts", got)
	}

	decision := guards.Evaluate(
		guards.Config{MaxPerHour: 2}, history, "restart-api", "deployment/payments/db", now)
	if decision.Allowed {
		t.Error("the hourly limit was forgotten across the restart")
	}
}

func TestHistoryLoader_SkipsWhatCannotBeReplayed(t *testing.T) {
	now := time.Now()

	// In flight: counts as a start, but has not completed.
	running := remediation("rem-running")
	running.CreationTimestamp = metav1.NewTime(now.Add(-time.Minute))
	running.Status.State = v1alpha1.RemediationStateRunning

	// Terminal but with no target: nothing to key a cooldown on.
	targetless := finished("rem-targetless", "", now.Add(-time.Minute), now)

	loader, history := newLoader(running, targetless)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if got := history.StartsSince("restart-api", now.Add(-time.Hour)); got != 2 {
		t.Errorf("StartsSince() = %d, want both records counted as starts", got)
	}
	if _, ok := history.LastCompletion("restart-api", ""); ok {
		t.Error("a record with no target produced a cooldown entry")
	}
}

func TestHistoryLoader_EmptyClusterIsFine(t *testing.T) {
	loader, history := newLoader()

	if err := loader.Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if starts, completions := history.Len(); starts != 0 || completions != 0 {
		t.Errorf("history = %d starts / %d completions, want empty", starts, completions)
	}
}

// Records older than the retention must not linger just because they were
// replayed rather than recorded live.
func TestHistoryLoader_PrunesStaleRecords(t *testing.T) {
	ancient := time.Now().Add(-72 * time.Hour)
	loader, history := newLoader(finished("rem-old", "deployment/payments/api", ancient, ancient))

	if err := loader.Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	if starts, completions := history.Len(); starts != 0 || completions != 0 {
		t.Errorf("history = %d starts / %d completions, want the stale record pruned", starts, completions)
	}
}

func TestHistoryLoader_ListFailureIsReported(t *testing.T) {
	loader, _ := newLoader()
	loader.Reader.(*fakeClient).listErr = errUnresolvable

	if err := loader.Load(context.Background()); err == nil {
		t.Error("Load() error = nil, want the list failure surfaced so startup fails loudly")
	}
}
