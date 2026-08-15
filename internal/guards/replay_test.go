package guards

import (
	"testing"
	"time"
)

// At startup the engine rebuilds this index from Remediation resources,
// replaying timestamps that are hours or days old. Those old timestamps
// must not be mistaken for the current time and expire the very records
// being loaded — the regression that made pruning explicit.
func TestMemoryHistory_ReplayDoesNotSelfExpire(t *testing.T) {
	h := NewMemoryHistory(time.Hour)

	// Replay in chronological order, oldest first, spanning three hours.
	for i := 3; i >= 0; i-- {
		at := base.Add(-time.Duration(i) * time.Hour)
		h.RecordStart("s", at)
		h.RecordCompletion("s", "deploy/api", at)
	}

	starts, completions := h.Len()
	if starts != 4 {
		t.Errorf("starts = %d, want 4: replay must not prune itself", starts)
	}
	if completions != 1 {
		t.Errorf("completions = %d, want 1", completions)
	}

	// The most recent completion survives and still drives the cooldown.
	last, ok := h.LastCompletion("s", "deploy/api")
	if !ok || !last.Equal(base) {
		t.Errorf("LastCompletion = (%v, %v), want (%v, true)", last, ok, base)
	}

	// Only an explicit Prune, against wall-clock now, expires anything.
	// With a one-hour retention the records at base-1h and base survive:
	// the cutoff is inclusive, matching StartsSince.
	h.Prune(base)
	if starts, _ := h.Len(); starts != 2 {
		t.Errorf("starts after Prune = %d, want 2", starts)
	}
}
