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
	// GuardGiveUp rejected the execution: remediation has run this many
	// times for this target inside the window without resolving the problem,
	// so remedik has stopped and said so.
	GuardGiveUp = "giveUpAfter"

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
	//
	// It is counted per strategy, across every target, which is deliberate:
	// it bounds what one strategy may do to a cluster. It is therefore not
	// the guard for "this one workload keeps breaking" — that is GiveUpAfter,
	// and without it a single flapping target consumes a strategy's whole
	// budget and stops it protecting everything else.
	MaxPerHour int

	// GiveUpAfter stops remediating a target that keeps needing it.
	//
	// The other guards pace: not yet, not this many, not safely. None of them
	// ever concludes anything, so remedik will restart the same Deployment for
	// ever. This one concludes.
	//
	// Zero count disables the check.
	GiveUpAfter GiveUp
}

// GiveUp is how many remediations of one target within what window mean that
// remediation is not the answer.
type GiveUp struct {
	// Count is how many remediations of the same (strategy, target) inside
	// Within are enough to stop. Zero disables the guard.
	Count int
	// Within is the window the count is measured over.
	//
	// It exists because remedik cannot see an alert stop firing, so it cannot
	// observe a streak being broken. A window is the honest form of "five
	// times in a row": five restarts of one Deployment in two hours means
	// restarting is not the fix; five over three months is a Tuesday.
	Within time.Duration
}

// History is the read model guards need. The engine implements it over
// Remediation resources; MemoryHistory implements it in memory.
type History interface {
	// LastCompletion returns when the strategy last finished on target.
	// The boolean is false when it never has.
	LastCompletion(strategy, target string) (time.Time, bool)
	// StartsSince counts executions of strategy started at or after since.
	StartsSince(strategy string, since time.Time) int
	// CompletionsSince counts how many times strategy finished on target at
	// or after since.
	CompletionsSince(strategy, target string, since time.Time) int
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
	if decision := evaluateRate(cfg, h, strategy, now); !decision.Allowed {
		return decision
	}
	return evaluateGiveUp(cfg, h, strategy, target, now)
}

// evaluateGiveUp asks whether remediation is working for this target.
//
// It is checked last because it is the most expensive answer to be wrong
// about: the others say "not yet", and this one says "not at all, and somebody
// is about to be paged". A cooldown or a rate limit that would have refused
// anyway should refuse first, quietly.
//
// It counts completions rather than failures, which is the whole point. The
// case this exists for is remediations that succeed: the rollout finishes, the
// pods come back ready, and twenty minutes later the alert is back. Counting
// failures would miss it entirely.
func evaluateGiveUp(cfg Config, h History, strategy, target string, now time.Time) Decision {
	if cfg.GiveUpAfter.Count <= 0 || cfg.GiveUpAfter.Within <= 0 || h == nil {
		return allowed
	}

	done := h.CompletionsSince(strategy, target, now.Add(-cfg.GiveUpAfter.Within))
	if done < cfg.GiveUpAfter.Count {
		return allowed
	}

	return Decision{
		Guard: GuardGiveUp,
		Reason: fmt.Sprintf(
			"%s has remediated %s %d times in the last %s and the problem keeps "+
				"coming back; remediation is not fixing this and it needs a person",
			strategy, target, done, cfg.GiveUpAfter.Within),
		// No RetryAfter: the wait is until the oldest completion ages out of
		// the window, and reporting a time would suggest remedik expects to
		// try again — which is exactly the impression this guard exists to
		// correct.
	}
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
