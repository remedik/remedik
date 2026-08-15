// Package guards decides whether a matched strategy is allowed to run
// right now.
//
// Matching answers "is there a remediation for this alert?"; guards answer
// "should it fire again, this soon, this often?". Two limits are supported
// in this version:
//
//   - cooldown — how long to wait after the same strategy finished on the
//     same target, so a flapping alert cannot restart a deployment in a
//     loop;
//   - maxPerHour — how many executions a strategy may start in the trailing
//     hour, so an alert storm cannot amplify into a cluster-wide event.
//
// Both are opt-in: a zero value means the limit is not enforced. Stopping
// a strategy entirely is `enabled: false`, never a zero limit — that
// distinction keeps an unset field from silently disabling remediation.
//
// Every decision takes an explicit `now`, so behavior is fully testable
// without sleeping or faking a clock.
package guards

import (
	"fmt"
	"time"
)

// Guard names. They are recorded on the Kubernetes event and used as
// metric labels, so the set is closed and stable.
const (
	// GuardCooldown rejected the execution: the same strategy finished on
	// the same target too recently.
	GuardCooldown = "cooldown"
	// GuardMaxPerHour rejected the execution: the strategy has started too
	// many executions in the trailing hour.
	GuardMaxPerHour = "maxPerHour"
)

// rateWindow is the trailing window maxPerHour is measured over.
const rateWindow = time.Hour

// Config holds a strategy's guard settings (spec.guards).
type Config struct {
	// Cooldown is the minimum time between completions and the next start
	// for the same (strategy, target). Zero disables the check.
	Cooldown time.Duration
	// MaxPerHour caps executions started by this strategy in the trailing
	// hour. Zero disables the check.
	MaxPerHour int
}

// History is the read model guards need. The engine implements it over
// Remediation resources; MemoryHistory implements it in memory.
type History interface {
	// LastCompletion returns when the strategy last finished on target.
	// The boolean is false when it never has.
	LastCompletion(strategy, target string) (time.Time, bool)
	// StartsSince counts executions of strategy started at or after since.
	StartsSince(strategy string, since time.Time) int
}

// Decision is the outcome of evaluating every guard.
type Decision struct {
	// Allowed reports whether the execution may start.
	Allowed bool
	// Guard names the guard that rejected it: GuardCooldown or
	// GuardMaxPerHour. Empty when allowed.
	Guard string
	// Reason is a human-readable explanation for logs, Kubernetes events
	// and Slack. Empty when allowed.
	Reason string
	// RetryAfter is how long until the rejecting guard would permit the
	// execution. Zero when allowed, or when the wait cannot be derived.
	RetryAfter time.Duration
}

// String renders the decision for logs.
func (d Decision) String() string {
	if d.Allowed {
		return "allowed"
	}
	return fmt.Sprintf("rejected by %s: %s", d.Guard, d.Reason)
}

// allowed is the permissive decision.
var allowed = Decision{Allowed: true}

// Evaluate applies every configured guard and returns the first rejection.
//
// Cooldown is checked before maxPerHour, so when both would reject, the
// reported guard is the target-specific one — the more actionable answer
// to "why didn't remedik fix this pod?".
func Evaluate(cfg Config, h History, strategy, target string, now time.Time) Decision {
	if d := evaluateCooldown(cfg, h, strategy, target, now); !d.Allowed {
		return d
	}
	return evaluateRate(cfg, h, strategy, now)
}

func evaluateCooldown(cfg Config, h History, strategy, target string, now time.Time) Decision {
	if cfg.Cooldown <= 0 || h == nil {
		return allowed
	}

	last, ok := h.LastCompletion(strategy, target)
	if !ok {
		return allowed
	}

	elapsed := now.Sub(last)
	if elapsed >= cfg.Cooldown {
		return allowed
	}
	// A completion timestamp in the future means clock skew or a bad
	// record; treating it as "still cooling down" is the safe reading.
	if elapsed < 0 {
		elapsed = 0
	}

	return Decision{
		Guard: GuardCooldown,
		Reason: fmt.Sprintf(
			"%s completed on %s %s ago, cooldown is %s",
			strategy, target, elapsed.Round(time.Second), cfg.Cooldown),
		RetryAfter: cfg.Cooldown - elapsed,
	}
}

func evaluateRate(cfg Config, h History, strategy string, now time.Time) Decision {
	if cfg.MaxPerHour <= 0 || h == nil {
		return allowed
	}

	started := h.StartsSince(strategy, now.Add(-rateWindow))
	if started < cfg.MaxPerHour {
		return allowed
	}

	return Decision{
		Guard: GuardMaxPerHour,
		Reason: fmt.Sprintf(
			"%s started %d executions in the last hour, limit is %d",
			strategy, started, cfg.MaxPerHour),
		// The wait depends on when the oldest start ages out of the
		// window, which History does not expose; the engine re-evaluates
		// on the next alert.
	}
}
