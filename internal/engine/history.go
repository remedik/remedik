package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/api/v1alpha1"
	"github.com/ratyx/remedik/internal/guards"
)

// HistoryLoader rebuilds the in-memory guard history from the Remediation
// resources already in the cluster.
//
// Without it, restarting the operator would forget every cooldown and
// hourly count, and the first alert after a restart would be remediated
// however recently the same thing had already been tried. Guards that
// evaporate on restart are worse than no guards: they are guards an
// operator believes in.
//
// The resources are the durable record; the in-memory index is a cache of
// them, and this is how the cache is warmed.
// It reads directly from the API server rather than through a cache, so it
// can run before the manager starts — guards must be warm before the first
// alert can be accepted, not merely soon after.
type HistoryLoader struct {
	Reader    client.Reader
	History   *guards.MemoryHistory
	Namespace string
	Logger    *slog.Logger
}

// Load replays every Remediation into the history.
func (l *HistoryLoader) Load(ctx context.Context) error {
	var list v1alpha1.RemediationList
	if err := l.Reader.List(ctx, &list, client.InNamespace(l.Namespace)); err != nil {
		return fmt.Errorf("list remediations: %w", err)
	}

	var starts, completions int
	for i := range list.Items {
		rem := &list.Items[i]

		if at, ok := startedAt(rem); ok {
			l.History.RecordStart(rem.Spec.StrategyName, at)
			starts++
		}

		if rem.Status.State.IsTerminal() && rem.Spec.Target != "" && rem.Status.CompletedAt != nil {
			l.History.RecordCompletion(rem.Spec.StrategyName, rem.Spec.Target, rem.Status.CompletedAt.Time)
			completions++
		}
	}

	// Replaying old records must not look like time has passed, which is
	// why MemoryHistory never expires anything on write. Prune once here,
	// against the real clock, so records outside the retention window do
	// not linger.
	l.History.Prune(time.Now())

	l.Logger.Info("guard history rebuilt from existing remediations",
		"remediations", len(list.Items), "starts", starts, "completions", completions)
	return nil
}

// startedAt reports when an execution began. A record that never started
// still counts against the hourly rate — it was created, which is the thing
// the rate limit bounds — so creation time is the fallback.
func startedAt(rem *v1alpha1.Remediation) (time.Time, bool) {
	if rem.Status.StartedAt != nil {
		return rem.Status.StartedAt.Time, true
	}
	if !rem.CreationTimestamp.IsZero() {
		return rem.CreationTimestamp.Time, true
	}
	return time.Time{}, false
}
