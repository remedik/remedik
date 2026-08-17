package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
)

// terminalRecord is a finished remediation for a strategy, completed n ago.
func terminalRecord(name, strategyName, target string, completedAgo time.Duration) *v1alpha1.Remediation {
	completed := metav1.NewTime(testClock.Add(-completedAgo))
	return &v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         testNamespace,
			CreationTimestamp: completed,
			Labels:            map[string]string{v1alpha1.LabelStrategy: strategyName},
		},
		Spec: v1alpha1.RemediationSpec{StrategyName: strategyName, Target: target},
		Status: v1alpha1.RemediationStatus{
			State:       v1alpha1.RemediationStateSucceeded,
			CompletedAt: &completed,
		},
	}
}

func newSweeper(t *testing.T, maxAge time.Duration, objs ...client.Object) (*Sweeper, *fakeClient, *countingRecorder) {
	t.Helper()

	c := newFakeClient(objs...)
	metrics := newCountingRecorder()
	return &Sweeper{
		Client:    c,
		Namespace: testNamespace,
		MaxAge:    maxAge,
		Metrics:   metrics,
		Logger:    quietLogger(),
		Now:       func() time.Time { return testClock },
	}, c, metrics
}

// The leak this exists for: pruning ran inside the terminal status write, so a
// strategy that was deleted, renamed, disabled or had merely gone quiet kept
// every record it ever made, for ever. Nothing would ever look at them again.
func TestSweeper_ReclaimsRecordsOfAStrategyThatNoLongerExists(t *testing.T) {
	old := terminalRecord("orphan-1", "deleted-strategy", "deployment/payments/api", 30*24*time.Hour)
	older := terminalRecord("orphan-2", "deleted-strategy", "deployment/payments/api", 60*24*time.Hour)

	// No RemediationStrategy objects at all: the strategy is gone.
	s, c, metrics := newSweeper(t, 7*24*time.Hour, old, older)

	if err := s.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	if got := len(c.remediations()); got != 0 {
		t.Errorf("records left = %d, want 0: nothing will ever prune these again", got)
	}
	if metrics.swept != 2 {
		t.Errorf("swept = %d, want 2", metrics.swept)
	}
}

// The case that would be an incident if it were got wrong. Guard state is
// rebuilt from records at startup, so a record inside a cooldown is not
// history -- it is the reason remedik will refuse to act again.
func TestSweeper_NeverDeletesWhatAGuardIsRelyingOn(t *testing.T) {
	strategy := &v1alpha1.RemediationStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: "slow-cooldown"},
		Spec: v1alpha1.RemediationStrategySpec{
			Guards: v1alpha1.Guards{
				Cooldown: &metav1.Duration{Duration: 24 * time.Hour},
			},
		},
	}
	// An hour old, and the retention says anything over a minute may go.
	recent := terminalRecord("recent", "slow-cooldown", "deployment/payments/api", time.Hour)

	s, c, metrics := newSweeper(t, time.Minute, strategy, recent)

	if err := s.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	if got := len(c.remediations()); got != 1 {
		t.Fatalf("records left = %d, want 1: deleting this makes remedik "+
			"remediate after the next restart something it had correctly refused", got)
	}
	if metrics.heldByGuards != 1 {
		t.Errorf("heldByGuards = %d, want 1: a policy that is not being applied "+
			"has to be countable", metrics.heldByGuards)
	}
}

// The floor follows the strategies as they are now, so lengthening a window
// widens it without a restart.
func TestSweeper_TheFloorFollowsTheLongestWindow(t *testing.T) {
	strategy := &v1alpha1.RemediationStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: "gives-up-slowly"},
		Spec: v1alpha1.RemediationStrategySpec{
			Guards: v1alpha1.Guards{
				Cooldown: &metav1.Duration{Duration: time.Minute},
				GiveUpAfter: &v1alpha1.GiveUpAfter{
					Count: 5, Within: metav1.Duration{Duration: 7 * 24 * time.Hour},
				},
			},
		},
	}
	inside := terminalRecord("inside", "gives-up-slowly", "deployment/payments/api", 24*time.Hour)
	outside := terminalRecord("outside", "gives-up-slowly", "deployment/payments/api", 30*24*time.Hour)

	s, c, _ := newSweeper(t, time.Hour, strategy, inside, outside)

	if err := s.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	left := c.remediations()
	if len(left) != 1 || left[0].Name != "inside" {
		t.Errorf("left = %v, want only the record inside the giveUpAfter window",
			names(left))
	}
}

// Work in flight is not history, whatever its age.
func TestSweeper_NeverSweepsWorkInFlight(t *testing.T) {
	pending := terminalRecord("pending", "s", "deployment/payments/api", 90*24*time.Hour)
	pending.Status.State = v1alpha1.RemediationStatePending
	pending.Status.CompletedAt = nil

	running := terminalRecord("running", "s", "deployment/payments/api", 90*24*time.Hour)
	running.Status.State = v1alpha1.RemediationStateRunning
	running.Status.CompletedAt = nil

	s, c, _ := newSweeper(t, time.Hour, pending, running)

	if err := s.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	if got := len(c.remediations()); got != 2 {
		t.Errorf("records left = %d, want 2: neither has finished", got)
	}
}

// An unset maximum age means today's behaviour exactly, so an upgrade cannot
// delete somebody's history because a default looked reasonable.
func TestSweeper_NoAgeLimitDeletesNothingByAge(t *testing.T) {
	ancient := terminalRecord("ancient", "s", "deployment/payments/api", 365*24*time.Hour)

	s, c, _ := newSweeper(t, 0, ancient)

	if err := s.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	if got := len(c.remediations()); got != 1 {
		t.Errorf("records left = %d, want 1: no age limit was configured", got)
	}
}

// The count applies to a quiet strategy too, which the completion-time prune
// could never do.
func TestSweeper_AppliesTheCountToAStrategyThatStopped(t *testing.T) {
	objs := make([]client.Object, 0, 20)
	for i := range 20 {
		objs = append(objs, terminalRecord(
			fmt.Sprintf("rem-%02d", i), "quiet", "deployment/payments/api",
			time.Duration(i+1)*time.Hour))
	}

	s, c, _ := newSweeper(t, 0, objs...)
	s.KeepPerStrategy = 5

	if err := s.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	left := c.remediations()
	if len(left) != 5 {
		t.Fatalf("records left = %d, want 5", len(left))
	}
	// The newest five, because the tail is what ages out.
	for _, rem := range left {
		if rem.Name > "rem-04" {
			t.Errorf("kept %s, want only the five most recent", rem.Name)
		}
	}
}

// Deleting in bulk makes watch events in bulk, and every controller reading
// this namespace pays for them.
func TestSweeper_DeletesAtABoundedRate(t *testing.T) {
	objs := make([]client.Object, 0, maxDeletesPerSweep+50)
	for i := range maxDeletesPerSweep + 50 {
		objs = append(objs, terminalRecord(
			fmt.Sprintf("rem-%04d", i), "s", "deployment/payments/api", 90*24*time.Hour))
	}

	s, c, metrics := newSweeper(t, time.Hour, objs...)

	if err := s.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	if metrics.swept != maxDeletesPerSweep {
		t.Errorf("swept = %d in one pass, want the bound of %d",
			metrics.swept, maxDeletesPerSweep)
	}
	if got := len(c.remediations()); got != 50 {
		t.Errorf("records left = %d, want 50 for the next pass", got)
	}
}

// It deletes without a remediation having happened, which is exactly what the
// lease exists to make single.
func TestSweeper_RunsOnlyOnTheLeader(t *testing.T) {
	s := &Sweeper{}
	if !s.NeedLeaderElection() {
		t.Error("NeedLeaderElection() = false; two instances would both sweep, " +
			"and deleting is the one thing here that happens without a remediation")
	}
}

func names(rems []*v1alpha1.Remediation) []string {
	out := make([]string, 0, len(rems))
	for _, r := range rems {
		out = append(out, r.Name)
	}
	return out
}
