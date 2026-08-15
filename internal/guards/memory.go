package guards

import (
	"sort"
	"sync"
	"time"
)

// DefaultRetention is how long MemoryHistory keeps records. It comfortably
// exceeds the one-hour rate window and typical cooldowns, while bounding
// memory during an alert storm.
const DefaultRetention = 24 * time.Hour

// MemoryHistory is an in-memory History.
//
// Remediation resources remain the durable record; this is the index the
// engine keeps hot, rebuilt from those resources at startup. It is safe for
// concurrent use.
//
// Writes never expire anything on their own: pruning is driven by wall
// clock time, which a record's own timestamp does not represent. Replaying
// yesterday's Remediation resources at startup must not look like a day has
// passed. The owner is therefore responsible for calling Prune periodically
// (the engine does so on a ticker); without it, memory grows with the
// number of distinct targets and executions.
type MemoryHistory struct {
	mu          sync.RWMutex
	retention   time.Duration
	completions map[completionKey]time.Time
	starts      map[string][]time.Time
}

type completionKey struct {
	strategy string
	target   string
}

// NewMemoryHistory returns an empty history. A retention of zero or less
// selects DefaultRetention.
func NewMemoryHistory(retention time.Duration) *MemoryHistory {
	if retention <= 0 {
		retention = DefaultRetention
	}
	return &MemoryHistory{
		retention:   retention,
		completions: make(map[completionKey]time.Time),
		starts:      make(map[string][]time.Time),
	}
}

// RecordStart notes that strategy started an execution at t.
func (h *MemoryHistory) RecordStart(strategy string, t time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.starts[strategy] = append(h.starts[strategy], t)

	// Deliveries can arrive out of order; keeping the slice sorted lets
	// StartsSince binary-search instead of scanning.
	starts := h.starts[strategy]
	if len(starts) > 1 && t.Before(starts[len(starts)-2]) {
		sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	}
}

// RecordCompletion notes that strategy finished on target at t. Only the
// most recent completion per (strategy, target) is kept, since that is all
// the cooldown check needs.
func (h *MemoryHistory) RecordCompletion(strategy, target string, t time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := completionKey{strategy: strategy, target: target}
	if existing, ok := h.completions[key]; !ok || t.After(existing) {
		h.completions[key] = t
	}
}

// LastCompletion implements History.
func (h *MemoryHistory) LastCompletion(strategy, target string) (time.Time, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	t, ok := h.completions[completionKey{strategy: strategy, target: target}]
	return t, ok
}

// StartsSince implements History.
func (h *MemoryHistory) StartsSince(strategy string, since time.Time) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	starts := h.starts[strategy]
	// starts is sorted ascending: find the first index at or after `since`.
	i := sort.Search(len(starts), func(i int) bool { return !starts[i].Before(since) })
	return len(starts) - i
}

// Prune drops records older than the retention, measured back from now.
// The engine calls it periodically; see the type documentation.
func (h *MemoryHistory) Prune(now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked(now)
}

// Len reports how many start records and completion records are held. It
// exists for tests and for a future debug endpoint.
func (h *MemoryHistory) Len() (starts, completions int) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, s := range h.starts {
		starts += len(s)
	}
	return starts, len(h.completions)
}

// pruneLocked drops expired records. The caller must hold the write lock.
func (h *MemoryHistory) pruneLocked(now time.Time) {
	cutoff := now.Add(-h.retention)

	for strategy, starts := range h.starts {
		i := sort.Search(len(starts), func(i int) bool { return !starts[i].Before(cutoff) })
		switch {
		case i == len(starts):
			delete(h.starts, strategy)
		case i > 0:
			// Re-slice into a fresh array so the dropped entries are
			// actually released rather than retained by the backing array.
			kept := make([]time.Time, len(starts)-i)
			copy(kept, starts[i:])
			h.starts[strategy] = kept
		}
	}

	for key, t := range h.completions {
		if t.Before(cutoff) {
			delete(h.completions, key)
		}
	}
}
