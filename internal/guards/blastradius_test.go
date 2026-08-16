package guards

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubWorkloads answers the guard's one question.
type stubWorkloads struct {
	workload   Workload
	applicable bool
	err        error
	calls      int
}

func (s *stubWorkloads) Workload(context.Context, string) (Workload, bool, error) {
	s.calls++
	return s.workload, s.applicable, s.err
}

func healthy(name string, desired, available int32) *stubWorkloads {
	return &stubWorkloads{
		workload:   Workload{Name: name, Desired: desired, Available: available},
		applicable: true,
	}
}

func TestBlastRadius_UnconfiguredNeverRefuses(t *testing.T) {
	reader := healthy("deployment/payments/api", 3, 0)

	d := EvaluateBlastRadius(context.Background(), BlastRadius{}, reader, "deployment/payments/api")

	if !d.Allowed {
		t.Errorf("decision = %s, want allowed when neither limit is set", d)
	}
	// Zero means unenforced, so the guard must not even ask: an unset field
	// should cost nothing, including a read.
	if reader.calls != 0 {
		t.Errorf("read the workload %d times for an unconfigured guard", reader.calls)
	}
}

func TestBlastRadius_MinAvailable(t *testing.T) {
	tests := []struct {
		name      string
		desired   int32
		available int32
		limit     int
		wantAllow bool
	}{
		{name: "the last one is protected", desired: 3, available: 1, limit: 1},
		{name: "below the limit is protected", desired: 3, available: 0, limit: 1},
		{name: "two left, one required", desired: 3, available: 2, limit: 1, wantAllow: true},
		{name: "fully available", desired: 3, available: 3, limit: 2, wantAllow: true},
		// A single-replica workload with minAvailable set can never be
		// remediated, which is what the setting means. Saying so is better
		// than pretending otherwise.
		{name: "a single replica with the guard on", desired: 1, available: 1, limit: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluateBlastRadius(context.Background(),
				BlastRadius{MinAvailable: tc.limit},
				healthy("deployment/payments/api", tc.desired, tc.available),
				"deployment/payments/api")

			if d.Allowed != tc.wantAllow {
				t.Fatalf("decision = %s, want allowed=%v", d, tc.wantAllow)
			}
			if tc.wantAllow {
				return
			}
			if d.Guard != GuardBlastRadius {
				t.Errorf("guard = %q, want %q", d.Guard, GuardBlastRadius)
			}
			if !strings.Contains(d.Reason, "deployment/payments/api") {
				t.Errorf("reason = %q, want it to name the workload", d.Reason)
			}
		})
	}
}

func TestBlastRadius_MaxUnavailablePercent(t *testing.T) {
	tests := []struct {
		name      string
		desired   int32
		available int32
		limit     int
		wantAllow bool
	}{
		{name: "half of four is over a quarter", desired: 4, available: 2, limit: 25},
		{name: "exactly the limit refuses", desired: 4, available: 3, limit: 25},
		{name: "under the limit is allowed", desired: 8, available: 7, limit: 25, wantAllow: true},
		{name: "nothing missing", desired: 4, available: 4, limit: 25, wantAllow: true},
		// A workload scaled to zero has nothing to protect; reporting it as
		// 100% unavailable would refuse every remediation on it for ever.
		{name: "scaled to zero", desired: 0, available: 0, limit: 25, wantAllow: true},
		// Rounding up, so a limit of 25% refuses at a quarter rather than
		// just past it: 1 of 3 is 34%, not 33%.
		{name: "one of three rounds up", desired: 3, available: 2, limit: 34},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluateBlastRadius(context.Background(),
				BlastRadius{MaxUnavailablePercent: tc.limit},
				healthy("statefulset/db/pg", tc.desired, tc.available),
				"statefulset/db/pg")

			if d.Allowed != tc.wantAllow {
				t.Fatalf("decision = %s, want allowed=%v (%d/%d, limit %d%%)",
					d, tc.wantAllow, tc.available, tc.desired, tc.limit)
			}
		})
	}
}

// A guard that permits an execution when it could not evaluate its own
// condition is not a guard, it is a comment.
func TestBlastRadius_FailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		reader WorkloadReader
	}{
		{
			name:   "the workload cannot be read",
			reader: &stubWorkloads{err: errors.New("deployments.apps is forbidden")},
		},
		{
			name: "there is no reader at all",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := EvaluateBlastRadius(context.Background(),
				BlastRadius{MinAvailable: 1}, tc.reader, "deployment/payments/api")

			if d.Allowed {
				t.Fatal("decision = allowed; a guard that cannot evaluate must refuse")
			}
			if d.Guard != GuardBlastRadius {
				t.Errorf("guard = %q, want %q", d.Guard, GuardBlastRadius)
			}
			// The refusal has to be diagnosable, or a missing permission
			// looks like remediation quietly stopping.
			if !strings.Contains(d.Reason, "guards.blastRadius.enabled") {
				t.Errorf("reason = %q, want it to name the permission to grant", d.Reason)
			}
		})
	}
}

// "Nothing to measure" is a different answer from "I could not measure it",
// and conflating them would make the fail-closed rule paralysing.
func TestBlastRadius_AllowsWhatItCannotMeasure(t *testing.T) {
	reader := &stubWorkloads{applicable: false}

	d := EvaluateBlastRadius(context.Background(),
		BlastRadius{MinAvailable: 1, MaxUnavailablePercent: 25}, reader, "node/aks-np1-0003")

	if !d.Allowed {
		t.Errorf("decision = %s, want allowed: a node has no replica count to protect", d)
	}
}

func TestWorkloadArithmetic(t *testing.T) {
	tests := []struct {
		name            string
		workload        Workload
		wantUnavailable int32
		wantPercent     int
	}{
		{name: "all up", workload: Workload{Desired: 4, Available: 4}, wantPercent: 0},
		{name: "half down", workload: Workload{Desired: 4, Available: 2}, wantUnavailable: 2, wantPercent: 50},
		{name: "one of three", workload: Workload{Desired: 3, Available: 2}, wantUnavailable: 1, wantPercent: 34},
		{name: "scaled to zero", workload: Workload{Desired: 0, Available: 0}, wantPercent: 0},
		{
			// More available than desired happens mid-rollout, with surge
			// pods counted. It is not "negative unavailability".
			name:     "surging",
			workload: Workload{Desired: 3, Available: 5},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.workload.Unavailable(); got != tc.wantUnavailable {
				t.Errorf("Unavailable() = %d, want %d", got, tc.wantUnavailable)
			}
			if got := tc.workload.UnavailablePercent(); got != tc.wantPercent {
				t.Errorf("UnavailablePercent() = %d, want %d", got, tc.wantPercent)
			}
		})
	}
}

func TestBlastRadiusConfigured(t *testing.T) {
	if (BlastRadius{}).Configured() {
		t.Error("an unset guard reads as configured")
	}
	if !(BlastRadius{MinAvailable: 1}).Configured() {
		t.Error("minAvailable alone does not read as configured")
	}
	if !(BlastRadius{MaxUnavailablePercent: 50}).Configured() {
		t.Error("maxUnavailablePercent alone does not read as configured")
	}
}
