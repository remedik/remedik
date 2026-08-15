package engine

import "time"

const (
	// baseBackoff is the wait before the first retry.
	baseBackoff = 30 * time.Second
	// maxBackoff caps the wait, so a strategy with many retries still
	// reacts within an incident rather than an afternoon.
	maxBackoff = 10 * time.Minute
)

// Backoff returns how long to wait before the given attempt.
//
// Attempt 1 is the first try and never waits. Each later attempt doubles
// the previous wait, capped at maxBackoff: 30s, 1m, 2m, 4m, 8m, 10m, 10m…
//
// The delay is deterministic — no jitter. A single-replica operator has no
// thundering herd to spread out, and predictable timing is easier to
// explain during an incident review.
func Backoff(attempt int32) time.Duration {
	if attempt <= 1 {
		return 0
	}

	d := baseBackoff
	for range attempt - 2 {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}
