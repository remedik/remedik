package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
)

// DefaultSweepInterval is how often retention is applied.
//
// Retention is a statement about the steady state, so it is enforced by
// something that runs in the steady state. Frequently enough that a cluster
// does not accumulate a day's excess, rarely enough that the LIST it makes is
// beside the point.
const DefaultSweepInterval = 30 * time.Minute

// maxDeletesPerSweep bounds one pass.
//
// Deleting in bulk makes watch events in bulk, and every controller and every
// dashboard reading this namespace pays for them. A sweep that takes several
// passes to catch up is fine; retention is not urgent by nature.
const maxDeletesPerSweep = 200

// floorMargin is added to the longest guard window before anything inside it
// is considered deletable.
//
// A strategy's cooldown can be lengthened between sweeps, and the records that
// would have covered the new window must still be there when it is.
const floorMargin = 2 * time.Hour

// Sweeper applies the retention policy on a schedule.
//
// It exists because pruning ran inside the terminal status write, and so only
// ever reclaimed records for the strategy that had just finished one. A
// strategy that was disabled, renamed, deleted, or had merely gone quiet kept
// everything it had ever made — and over the life of a cluster, strategies are
// added and removed, so each departure left a permanent deposit.
//
// A timer converges where more hooks would not: whatever state the cluster
// reached and however it got there, the next sweep applies the policy.
type Sweeper struct {
	// Client lists and deletes records. Required.
	Client client.Client
	// Namespace is where Remediation records live.
	Namespace string
	// MaxAge deletes terminal records older than this, whatever the
	// per-strategy count allows. Zero disables the age limit and leaves the
	// count as the only policy, which is what an operator who set nothing
	// already had.
	MaxAge time.Duration
	// KeepPerStrategy is the count the reconciler already enforces. The sweep
	// applies it too, so a strategy that stopped completing anything is not
	// left holding its last few hundred for ever.
	KeepPerStrategy int
	// Every is the sweep interval. Zero means DefaultSweepInterval.
	Every time.Duration
	// Metrics records what each sweep did. Optional.
	Metrics Recorder
	// Logger is required.
	Logger *slog.Logger
	// Now supplies the clock; tests inject one.
	Now func() time.Time
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
//
// True, unlike the HTTP servers. This is the one thing here that deletes
// without a remediation having happened, and "exactly one instance acts" is
// the rule the lease exists to enforce.
func (s *Sweeper) NeedLeaderElection() bool { return true }

// Start implements manager.Runnable.
func (s *Sweeper) Start(ctx context.Context) error {
	every := s.Every
	if every <= 0 {
		every = DefaultSweepInterval
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	s.Logger.Info("retention sweeper started",
		"every", every, "max_age", s.MaxAge, "keep_per_strategy", s.KeepPerStrategy)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.Sweep(ctx); err != nil {
				// A failed sweep is not fatal: retention is not urgent, and
				// the next tick tries again. Stopping the operator because
				// housekeeping failed would be the wrong trade.
				s.Logger.Error("retention sweep failed", "err", err)
			}
		}
	}
}

// Sweep applies the policy once.
func (s *Sweeper) Sweep(ctx context.Context) error {
	now := s.now()

	floor, err := s.guardFloor(ctx, now)
	if err != nil {
		return fmt.Errorf("compute the guard floor: %w", err)
	}

	var list v1alpha1.RemediationList
	if err := s.Client.List(ctx, &list,
		client.InNamespace(s.Namespace), client.UnsafeDisableDeepCopy); err != nil {
		return fmt.Errorf("list remediations: %w", err)
	}

	candidates, heldByFloor := s.candidates(list.Items, now, floor)
	if heldByFloor > 0 && s.MaxAge > 0 {
		// Said out loud once per sweep: a retention policy that is quietly not
		// being applied is worse than one that is refused.
		s.Logger.Info("retention held back by the guards",
			"records", heldByFloor, "floor", now.Sub(floor).Round(time.Minute),
			"max_age", s.MaxAge)
	}

	deleted := 0
	for _, rem := range candidates {
		if deleted == maxDeletesPerSweep {
			s.Logger.Info("sweep reached its per-pass limit; the rest go next time",
				"deleted", deleted, "remaining", len(candidates)-deleted)
			break
		}
		if err := s.Client.Delete(ctx, rem); err != nil {
			if isNotFound(err) {
				continue
			}
			return fmt.Errorf("delete %s: %w", rem.Name, err)
		}
		deleted++
	}

	if s.Metrics != nil {
		s.Metrics.RecordsSwept(deleted, heldByFloor)
	}
	if deleted > 0 {
		s.Logger.Info("retention sweep reclaimed records",
			"deleted", deleted, "held_by_guards", heldByFloor, "total", len(list.Items))
	}
	return nil
}

// candidates picks what may go, and counts what the floor saved.
//
// Two policies, applied together: anything older than MaxAge, and anything
// beyond KeepPerStrategy for its strategy. The floor overrides both.
func (s *Sweeper) candidates(
	items []v1alpha1.Remediation, now, floor time.Time,
) (out []*v1alpha1.Remediation, heldByFloor int) {
	byStrategy := make(map[string][]*v1alpha1.Remediation)

	for i := range items {
		rem := &items[i]

		// Work in flight is not history. A record that has not finished is
		// never a candidate, however old it looks.
		if !rem.Status.State.IsTerminal() {
			continue
		}
		byStrategy[rem.Spec.StrategyName] = append(byStrategy[rem.Spec.StrategyName], rem)
	}

	for _, group := range byStrategy {
		// Newest first, so the tail is what ages out. Completion, not
		// creation: when the thing happened is what retention is about.
		sort.Slice(group, func(i, j int) bool {
			return completedAt(group[i]).After(completedAt(group[j]))
		})

		for i, rem := range group {
			tooOld := s.MaxAge > 0 && completedAt(rem).Before(now.Add(-s.MaxAge))
			tooMany := s.KeepPerStrategy > 0 && i >= s.KeepPerStrategy
			if !tooOld && !tooMany {
				continue
			}
			// The floor wins over both. A record inside a guard window is not
			// history; it is the reason remedik will refuse to act again, and
			// deleting it means the next restart remediates something it had
			// correctly refused.
			if completedAt(rem).After(floor) {
				heldByFloor++
				continue
			}
			out = append(out, rem)
		}
	}
	return out, heldByFloor
}

// guardFloor is the oldest moment a record may be deleted from.
//
// Computed from the strategies as they are now, so lengthening a cooldown
// widens the floor without a restart.
func (s *Sweeper) guardFloor(ctx context.Context, now time.Time) (time.Time, error) {
	var list v1alpha1.RemediationStrategyList
	if err := s.Client.List(ctx, &list, client.UnsafeDisableDeepCopy); err != nil {
		return time.Time{}, err
	}

	longest := time.Duration(0)
	for i := range list.Items {
		guards := list.Items[i].Spec.Guards

		if d := guards.Cooldown; d != nil && d.Duration > longest {
			longest = d.Duration
		}
		// maxPerHour is measured over an hour, so an hour of starts matters.
		if guards.MaxPerHour > 0 && time.Hour > longest {
			longest = time.Hour
		}
		if g := guards.GiveUpAfter; g != nil && g.Within.Duration > longest {
			longest = g.Within.Duration
		}
	}

	return now.Add(-(longest + floorMargin)), nil
}

// completedAt is when the record finished, falling back to creation for a
// terminal record that somehow carries no completion time.
func completedAt(rem *v1alpha1.Remediation) time.Time {
	if rem.Status.CompletedAt != nil {
		return rem.Status.CompletedAt.Time
	}
	return rem.CreationTimestamp.Time
}

func (s *Sweeper) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
