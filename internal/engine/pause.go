package engine

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PauseKey is the ConfigMap key that stops remedik acting.
//
// A ConfigMap rather than a chart value, because the two are needed for
// different moments. A value is how you configure a cluster; this is how you
// stop it at three in the morning:
//
//	kubectl -n remedik patch configmap remedik-pause \
//	  --type merge -p '{"data":{"paused":"true"}}'
//
// It takes effect on the next alert, with no restart and no rollout, and it is
// as auditable as any other object — GitOps sees it, `kubectl describe` shows
// who set it and when.
const PauseKey = "paused"

// PauseReasonKey is an optional note that ends up on every record made while
// paused, so somebody reading the audit trail a week later learns why rather
// than finding an unexplained run of simulations.
const PauseReasonKey = "reason"

// DefaultPausePollInterval is how often the switch is re-read.
//
// Polled rather than watched: a watch would need an informer and a cache for
// one key, and the thing being optimised here is how few moving parts stand
// between a person and stopping remediation. Five seconds is faster than
// anybody can open the next terminal.
const DefaultPausePollInterval = 5 * time.Second

// Pause is the runtime kill switch.
//
// It does not silence remedik — it forces dry-run everywhere. That distinction
// is the whole design.
//
// Refusing alerts outright would mean the one time an operator most wants to
// know what remediation would have done, the record does not exist. Forcing
// dry-run keeps every record, marked Simulated and carrying the plan, so
// unpausing is an informed decision rather than a hopeful one, and the pause
// itself has an audit trail.
//
// Safe for concurrent use: the gateway reads it per alert while the poller
// writes it.
type Pause struct {
	paused atomic.Bool
	reason atomic.Pointer[string]
}

// Paused reports whether remediation is currently held.
func (p *Pause) Paused() bool {
	if p == nil {
		return false
	}
	return p.paused.Load()
}

// Reason is the note left with the pause, if any.
func (p *Pause) Reason() string {
	if p == nil {
		return ""
	}
	if r := p.reason.Load(); r != nil {
		return *r
	}
	return ""
}

// set updates the switch and reports whether it changed.
func (p *Pause) set(paused bool, reason string) bool {
	p.reason.Store(&reason)
	return p.paused.Swap(paused) != paused
}

// PauseWatcher keeps a Pause current from a ConfigMap.
type PauseWatcher struct {
	// Reader reads the ConfigMap. Required.
	Reader client.Reader
	// Namespace and Name locate it.
	Namespace string
	Name      string
	// Pause is what gets updated. Required.
	Pause *Pause
	// Every is the poll interval. Zero means DefaultPausePollInterval.
	Every time.Duration
	// Logger is required.
	Logger *slog.Logger
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
//
// False. Every replica must know it is paused, not only the one holding the
// lease: the gateway answers on all of them, and a standby that took over
// believing remediation was enabled would act on the first alert it saw.
func (w *PauseWatcher) NeedLeaderElection() bool { return false }

// Start implements manager.Runnable.
func (w *PauseWatcher) Start(ctx context.Context) error {
	every := w.Every
	if every <= 0 {
		every = DefaultPausePollInterval
	}

	// Read once before the first tick, so an operator that starts while paused
	// is paused from its first alert rather than for one interval.
	w.poll(ctx)

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *PauseWatcher) poll(ctx context.Context) {
	var cm corev1.ConfigMap
	key := client.ObjectKey{Namespace: w.Namespace, Name: w.Name}

	if err := w.Reader.Get(ctx, key, &cm); err != nil {
		// A missing ConfigMap means not paused, which is the safe reading of
		// absence for a switch whose "on" position stops the product working.
		// A read failure is logged and changes nothing: flipping to paused
		// because the API server hiccuped would be a self-inflicted outage of
		// remediation, and flipping to unpaused would ignore somebody's
		// deliberate stop. Keeping the last known answer does neither.
		if isNotFound(err) {
			if w.Pause.set(false, "") {
				w.Logger.Info("remediation resumed: the pause ConfigMap is gone")
			}
			return
		}
		w.Logger.Warn("could not read the pause switch; keeping the last known state",
			"paused", w.Pause.Paused(), "err", err)
		return
	}

	paused := parsePaused(cm.Data[PauseKey])
	reason := strings.TrimSpace(cm.Data[PauseReasonKey])

	if !w.Pause.set(paused, reason) {
		return
	}
	if paused {
		w.Logger.Warn("remediation is PAUSED: every strategy will only report, "+
			"whatever its posture says", "reason", reason)
		return
	}
	w.Logger.Info("remediation resumed")
}

// parsePaused reads the key generously.
//
// "true", "yes" and "1" all mean paused, because somebody typing this during an
// incident should not have to remember which. Anything unrecognised means not
// paused: a switch that stopped remediation because of a typo would be worse
// than one that ignored it, and the log line says which state is in force
// either way.
func parsePaused(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "on":
		return true
	}
	paused, err := strconv.ParseBool(strings.TrimSpace(value))
	return err == nil && paused
}
