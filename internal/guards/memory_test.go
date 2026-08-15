package guards

import (
	"sync"
	"testing"
	"time"
)

func TestMemoryHistory_Completions(t *testing.T) {
	h := NewMemoryHistory(0)

	if _, ok := h.LastCompletion("s", "t"); ok {
		t.Error("LastCompletion ok = true on an empty history")
	}

	h.RecordCompletion("s", "t", base)
	got, ok := h.LastCompletion("s", "t")
	if !ok {
		t.Fatal("LastCompletion ok = false after RecordCompletion")
	}
	if !got.Equal(base) {
		t.Errorf("LastCompletion = %v, want %v", got, base)
	}

	t.Run("keeps the most recent completion", func(t *testing.T) {
		h.RecordCompletion("s", "t", base.Add(time.Minute))
		got, _ := h.LastCompletion("s", "t")
		if !got.Equal(base.Add(time.Minute)) {
			t.Errorf("LastCompletion = %v, want the newer timestamp", got)
		}
	})

	t.Run("an older record does not overwrite a newer one", func(t *testing.T) {
		h.RecordCompletion("s", "t", base.Add(-time.Hour))
		got, _ := h.LastCompletion("s", "t")
		if !got.Equal(base.Add(time.Minute)) {
			t.Errorf("LastCompletion = %v, want the newer timestamp to survive", got)
		}
	})

	t.Run("targets are tracked independently", func(t *testing.T) {
		if _, ok := h.LastCompletion("s", "other-target"); ok {
			t.Error("a different target reported a completion")
		}
		if _, ok := h.LastCompletion("other-strategy", "t"); ok {
			t.Error("a different strategy reported a completion")
		}
	})
}

func TestMemoryHistory_StartsSince(t *testing.T) {
	h := NewMemoryHistory(0)
	for i := range 5 {
		// 5 starts, one every 10 minutes, ending at base.
		h.RecordStart("s", base.Add(-time.Duration(i)*10*time.Minute))
	}

	tests := []struct {
		name  string
		since time.Time
		want  int
	}{
		{name: "all of them", since: base.Add(-time.Hour), want: 5},
		{name: "last 25 minutes", since: base.Add(-25 * time.Minute), want: 3},
		{name: "boundary is inclusive", since: base.Add(-20 * time.Minute), want: 3},
		{name: "only the newest", since: base, want: 1},
		{name: "none in the future", since: base.Add(time.Minute), want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.StartsSince("s", tc.since); got != tc.want {
				t.Errorf("StartsSince() = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("unknown strategy", func(t *testing.T) {
		if got := h.StartsSince("nope", base.Add(-time.Hour)); got != 0 {
			t.Errorf("StartsSince() = %d, want 0", got)
		}
	})
}

func TestMemoryHistory_HandlesOutOfOrderStarts(t *testing.T) {
	h := NewMemoryHistory(0)

	// Deliveries can arrive out of order; the count must not depend on it.
	h.RecordStart("s", base)
	h.RecordStart("s", base.Add(-30*time.Minute))
	h.RecordStart("s", base.Add(-10*time.Minute))

	if got := h.StartsSince("s", base.Add(-20*time.Minute)); got != 2 {
		t.Errorf("StartsSince() = %d, want 2", got)
	}
	if got := h.StartsSince("s", base.Add(-time.Hour)); got != 3 {
		t.Errorf("StartsSince() = %d, want 3", got)
	}
}

func TestMemoryHistory_PruneDropsExpiredRecords(t *testing.T) {
	h := NewMemoryHistory(time.Hour)

	h.RecordStart("s", base.Add(-3*time.Hour))
	h.RecordStart("s", base.Add(-2*time.Hour))
	h.RecordStart("s", base.Add(-30*time.Minute))
	h.RecordCompletion("s", "old-target", base.Add(-5*time.Hour))
	h.RecordCompletion("s", "fresh-target", base.Add(-10*time.Minute))

	starts, completions := h.Len()
	if starts != 3 || completions != 2 {
		t.Fatalf("before prune: starts = %d, completions = %d, want 3 and 2", starts, completions)
	}

	h.Prune(base)

	starts, completions = h.Len()
	if starts != 1 {
		t.Errorf("after prune: starts = %d, want 1", starts)
	}
	if completions != 1 {
		t.Errorf("after prune: completions = %d, want 1", completions)
	}
	if _, ok := h.LastCompletion("s", "old-target"); ok {
		t.Error("expired completion survived the prune")
	}
	if _, ok := h.LastCompletion("s", "fresh-target"); !ok {
		t.Error("fresh completion was pruned")
	}
}

func TestMemoryHistory_PruneRemovesEmptyStrategies(t *testing.T) {
	h := NewMemoryHistory(time.Hour)
	h.RecordStart("gone", base.Add(-5*time.Hour))

	h.Prune(base)

	if starts, _ := h.Len(); starts != 0 {
		t.Errorf("starts = %d, want 0", starts)
	}
	if got := h.StartsSince("gone", base.Add(-24*time.Hour)); got != 0 {
		t.Errorf("StartsSince() = %d, want 0", got)
	}
}

func TestMemoryHistory_DefaultRetention(t *testing.T) {
	h := NewMemoryHistory(0)
	if h.retention != DefaultRetention {
		t.Errorf("retention = %v, want %v", h.retention, DefaultRetention)
	}

	negative := NewMemoryHistory(-time.Hour)
	if negative.retention != DefaultRetention {
		t.Errorf("retention = %v, want %v", negative.retention, DefaultRetention)
	}
}

// The engine calls into history from concurrent reconciles; the race
// detector makes this test meaningful.
func TestMemoryHistory_ConcurrentAccess(t *testing.T) {
	h := NewMemoryHistory(0)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(3)
		go func() { defer wg.Done(); h.RecordStart("s", base.Add(time.Duration(i)*time.Second)) }()
		go func() {
			defer wg.Done()
			h.RecordCompletion("s", "t", base.Add(time.Duration(i)*time.Second))
		}()
		go func() {
			defer wg.Done()
			_ = h.StartsSince("s", base.Add(-time.Hour))
			_, _ = h.LastCompletion("s", "t")
		}()
	}
	wg.Wait()

	if got := h.StartsSince("s", base.Add(-time.Hour)); got != 50 {
		t.Errorf("StartsSince() = %d, want 50", got)
	}
}

// MemoryHistory must satisfy the interface guards evaluate against.
func TestMemoryHistory_ImplementsHistory(t *testing.T) {
	var h History = NewMemoryHistory(0)

	memory, ok := h.(*MemoryHistory)
	if !ok {
		t.Fatal("type assertion failed")
	}
	memory.RecordCompletion("s", "deploy/api", base.Add(-time.Minute))

	got := Evaluate(Config{Cooldown: 15 * time.Minute}, h, "s", "deploy/api", base)
	if got.Allowed {
		t.Errorf("Allowed = true, want the cooldown to reject (%s)", got)
	}
}
