package engine

import (
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	tests := []struct {
		attempt int32
		want    time.Duration
	}{
		{attempt: 0, want: 0},
		{attempt: 1, want: 0},
		{attempt: 2, want: 30 * time.Second},
		{attempt: 3, want: time.Minute},
		{attempt: 4, want: 2 * time.Minute},
		{attempt: 5, want: 4 * time.Minute},
		{attempt: 6, want: 8 * time.Minute},
		{attempt: 7, want: maxBackoff},
		{attempt: 50, want: maxBackoff},
	}

	for _, tc := range tests {
		if got := Backoff(tc.attempt); got != tc.want {
			t.Errorf("Backoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestBackoff_NeverExceedsCap(t *testing.T) {
	for attempt := int32(0); attempt < 100; attempt++ {
		if got := Backoff(attempt); got > maxBackoff {
			t.Fatalf("Backoff(%d) = %v, over the %v cap", attempt, got, maxBackoff)
		}
	}
}
