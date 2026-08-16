package guards

import (
	"context"
	"fmt"
)

// GuardBlastRadius rejected the execution: the workload is already too
// degraded to touch.
const GuardBlastRadius = "blastRadius"

// BlastRadius bounds how broken a workload may already be before remedik
// adds to it.
//
// The other two guards ask questions about time — how recently, how often.
// This one asks about state, and it exists because the actions that remove
// capacity rather than replacing it need bounding by somebody other than the
// workload's owner. A PodDisruptionBudget is the right mechanism and the
// wrong coverage: most workloads have none, and the ones that do have one
// written by a person who was not thinking about an automated system acting
// unattended at 3am.
//
// Both limits are opt-in. Zero means unenforced, as everywhere else in this
// package: stopping a strategy is `enabled: false`, never a zero limit.
type BlastRadius struct {
	// MinAvailable refuses while the workload has this many available
	// replicas or fewer. "Never touch the last one."
	MinAvailable int
	// MaxUnavailablePercent refuses while at least this share of the
	// workload is already unavailable. "Do not add to something already
	// struggling."
	MaxUnavailablePercent int
}

// Configured reports whether either limit is set.
func (b BlastRadius) Configured() bool {
	return b.MinAvailable > 0 || b.MaxUnavailablePercent > 0
}

// Workload is how much of something is up. It is a plain struct so that this
// package keeps knowing nothing about Kubernetes: the engine reads the
// cluster and reports the two numbers that matter.
type Workload struct {
	// Name is how the workload is referred to in a refusal, such as
	// "deployment/payments/api".
	Name string
	// Desired is how many replicas there should be.
	Desired int32
	// Available is how many are available now.
	Available int32
}

// Unavailable is how many replicas are missing.
func (w Workload) Unavailable() int32 {
	if w.Available >= w.Desired {
		return 0
	}
	return w.Desired - w.Available
}

// UnavailablePercent is the share of the workload that is not available,
// rounded up so that a limit of 25% actually refuses at a quarter rather
// than just past it.
func (w Workload) UnavailablePercent() int {
	if w.Desired <= 0 {
		// Nothing is wanted, so nothing is missing. A workload scaled to
		// zero has nothing to protect.
		return 0
	}
	missing := int(w.Unavailable())
	return (missing*100 + int(w.Desired) - 1) / int(w.Desired)
}

// WorkloadReader answers how much of the workload behind a target is up.
//
// Implementations return applicable=false when there is no workload to
// measure — a node, or an action that touches nothing in the cluster — which
// is a different answer from an error. An error means the guard could not
// evaluate its own condition, and the guard refuses in that case.
type WorkloadReader interface {
	Workload(ctx context.Context, target string) (workload Workload, applicable bool, err error)
}

// EvaluateBlastRadius applies the guard.
//
// It is separate from Evaluate because it is the only guard that has to look
// at the cluster, and therefore the only one that can fail for reasons that
// have nothing to do with the strategy.
func EvaluateBlastRadius(
	ctx context.Context, cfg BlastRadius, reader WorkloadReader, target string,
) Decision {
	if !cfg.Configured() {
		return allowed
	}
	if reader == nil {
		// Configured but unreadable: the same situation as a permission
		// that was never granted, and the same answer.
		return refuseUnreadable(target, fmt.Errorf("no workload reader is configured"))
	}

	workload, applicable, err := reader.Workload(ctx, target)
	if err != nil {
		return refuseUnreadable(target, err)
	}
	if !applicable {
		// A node has no replica count; an action that touches nothing has no
		// workload. There is nothing here for this guard to measure, which
		// is not the same as failing to measure it.
		return allowed
	}

	if cfg.MinAvailable > 0 && int(workload.Available) <= cfg.MinAvailable {
		return Decision{
			Guard: GuardBlastRadius,
			Reason: fmt.Sprintf(
				"%s has %d of %d replicas available, and blastRadius.minAvailable is %d: "+
					"acting now could take it below what you said it must keep",
				workload.Name, workload.Available, workload.Desired, cfg.MinAvailable),
		}
	}

	if cfg.MaxUnavailablePercent > 0 {
		if got := workload.UnavailablePercent(); got >= cfg.MaxUnavailablePercent {
			return Decision{
				Guard: GuardBlastRadius,
				Reason: fmt.Sprintf(
					"%d%% of %s is already unavailable (%d of %d replicas), and "+
						"blastRadius.maxUnavailablePercent is %d: it is already struggling "+
						"without remedik adding to it",
					got, workload.Name, workload.Unavailable(), workload.Desired,
					cfg.MaxUnavailablePercent),
			}
		}
	}

	return allowed
}

// refuseUnreadable is the fail-closed path.
//
// A guard that permits an execution when it could not evaluate its own
// condition is not a guard, it is a comment. Refusing is loud — the reason
// reaches an event on the strategy — where allowing would be a cluster
// quietly acting unbounded.
func refuseUnreadable(target string, err error) Decision {
	return Decision{
		Guard: GuardBlastRadius,
		Reason: fmt.Sprintf(
			"cannot tell how much of %s is available, so remedik will not act on it: %v. "+
				"blastRadius needs read access to the workload; the chart grants it with "+
				"guards.blastRadius.enabled=true",
			target, err),
	}
}
