package guards

import (
	"strings"
	"testing"
	"time"
)

var base = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

// fakeHistory answers exactly what a test sets up.
type fakeHistory struct {
	lastCompletion time.Time
	hasCompletion  bool
	startsSince    int
	// sinceSeen records the cutoff StartsSince was called with.
	sinceSeen time.Time
}

func (f *fakeHistory) LastCompletion(_, _ string) (time.Time, bool) {
	return f.lastCompletion, f.hasCompletion
}

func (f *fakeHistory) StartsSince(_ string, since time.Time) int {
	f.sinceSeen = since
	return f.startsSince
}

func TestEvaluate_Cooldown(t *testing.T) {
	tests := []struct {
		name           string
		cooldown       time.Duration
		lastCompletion time.Time
		hasCompletion  bool
		wantAllowed    bool
		wantRetryAfter time.Duration
	}{
		{
			name:           "blocks a re-fire inside the cooldown",
			cooldown:       15 * time.Minute,
			lastCompletion: base.Add(-5 * time.Minute),
			hasCompletion:  true,
			wantAllowed:    false,
			wantRetryAfter: 10 * time.Minute,
		},
		{
			name:           "allows once the cooldown has passed",
			cooldown:       15 * time.Minute,
			lastCompletion: base.Add(-20 * time.Minute),
			hasCompletion:  true,
			wantAllowed:    true,
		},
		{
			name:           "boundary: exactly the cooldown is allowed",
			cooldown:       15 * time.Minute,
			lastCompletion: base.Add(-15 * time.Minute),
			hasCompletion:  true,
			wantAllowed:    true,
		},
		{
			name:          "allows when the strategy never ran on this target",
			cooldown:      15 * time.Minute,
			hasCompletion: false,
			wantAllowed:   true,
		},
		{
			name:           "zero cooldown disables the check",
			cooldown:       0,
			lastCompletion: base.Add(-time.Second),
			hasCompletion:  true,
			wantAllowed:    true,
		},
		{
			name:           "a future completion is treated as still cooling down",
			cooldown:       15 * time.Minute,
			lastCompletion: base.Add(time.Minute), // clock skew
			hasCompletion:  true,
			wantAllowed:    false,
			wantRetryAfter: 15 * time.Minute,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHistory{lastCompletion: tc.lastCompletion, hasCompletion: tc.hasCompletion}

			got := Evaluate(Config{Cooldown: tc.cooldown}, h, "restart-api", "deploy/api", base)

			if got.Allowed != tc.wantAllowed {
				t.Fatalf("Allowed = %v, want %v (%s)", got.Allowed, tc.wantAllowed, got)
			}
			if tc.wantAllowed {
				if got.Guard != "" || got.Reason != "" {
					t.Errorf("allowed decision carries guard %q / reason %q", got.Guard, got.Reason)
				}
				return
			}
			if got.Guard != GuardCooldown {
				t.Errorf("Guard = %q, want %q", got.Guard, GuardCooldown)
			}
			if got.RetryAfter != tc.wantRetryAfter {
				t.Errorf("RetryAfter = %v, want %v", got.RetryAfter, tc.wantRetryAfter)
			}
			if !strings.Contains(got.Reason, "cooldown") {
				t.Errorf("Reason = %q, want it to mention the cooldown", got.Reason)
			}
		})
	}
}

func TestEvaluate_MaxPerHour(t *testing.T) {
	tests := []struct {
		name        string
		maxPerHour  int
		startsSince int
		wantAllowed bool
	}{
		{name: "under the limit", maxPerHour: 3, startsSince: 2, wantAllowed: true},
		{name: "at the limit is rejected", maxPerHour: 3, startsSince: 3, wantAllowed: false},
		{name: "over the limit is rejected", maxPerHour: 3, startsSince: 9, wantAllowed: false},
		{name: "zero disables the check", maxPerHour: 0, startsSince: 100, wantAllowed: true},
		{name: "negative disables the check", maxPerHour: -1, startsSince: 100, wantAllowed: true},
		{name: "no history yet", maxPerHour: 1, startsSince: 0, wantAllowed: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHistory{startsSince: tc.startsSince}

			got := Evaluate(Config{MaxPerHour: tc.maxPerHour}, h, "restart-api", "deploy/api", base)

			if got.Allowed != tc.wantAllowed {
				t.Fatalf("Allowed = %v, want %v (%s)", got.Allowed, tc.wantAllowed, got)
			}
			if tc.wantAllowed {
				return
			}
			if got.Guard != GuardMaxPerHour {
				t.Errorf("Guard = %q, want %q", got.Guard, GuardMaxPerHour)
			}
			if !strings.Contains(got.Reason, "limit is 3") {
				t.Errorf("Reason = %q, want it to state the limit", got.Reason)
			}
		})
	}
}

func TestEvaluate_RateWindowIsTheTrailingHour(t *testing.T) {
	h := &fakeHistory{startsSince: 0}

	Evaluate(Config{MaxPerHour: 5}, h, "restart-api", "deploy/api", base)

	if want := base.Add(-time.Hour); !h.sinceSeen.Equal(want) {
		t.Errorf("StartsSince called with %v, want %v", h.sinceSeen, want)
	}
}

func TestEvaluate_CooldownIsReportedBeforeRate(t *testing.T) {
	// Both guards would reject; the target-specific one is the useful
	// answer to "why didn't remedik fix this?".
	h := &fakeHistory{
		lastCompletion: base.Add(-time.Minute),
		hasCompletion:  true,
		startsSince:    99,
	}

	got := Evaluate(Config{Cooldown: time.Hour, MaxPerHour: 1}, h, "restart-api", "deploy/api", base)

	if got.Allowed {
		t.Fatal("Allowed = true, want a rejection")
	}
	if got.Guard != GuardCooldown {
		t.Errorf("Guard = %q, want %q", got.Guard, GuardCooldown)
	}
}

func TestEvaluate_NoGuardsConfigured(t *testing.T) {
	got := Evaluate(Config{}, &fakeHistory{}, "restart-api", "deploy/api", base)
	if !got.Allowed {
		t.Errorf("Allowed = false (%s), want true when no guards are set", got)
	}
}

func TestEvaluate_NilHistoryIsSafe(t *testing.T) {
	got := Evaluate(Config{Cooldown: time.Hour, MaxPerHour: 1}, nil, "s", "t", base)
	if !got.Allowed {
		t.Errorf("Allowed = false (%s), want true when no history is available", got)
	}
}

func TestDecision_String(t *testing.T) {
	if got := (Decision{Allowed: true}).String(); got != "allowed" {
		t.Errorf("String() = %q, want %q", got, "allowed")
	}

	d := Decision{Guard: GuardCooldown, Reason: "too soon"}
	if got, want := d.String(), "rejected by cooldown: too soon"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
