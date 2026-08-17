package main

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func quietTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{in: "debug", want: slog.LevelDebug},
		{in: "info", want: slog.LevelInfo},
		{in: "INFO", want: slog.LevelInfo},
		{in: " warn ", want: slog.LevelWarn},
		{in: "warning", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "trace", wantErr: true},
		{in: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseLogLevel(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseLogLevel(%q) error = nil, want an error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLogLevel(%q) error = %v, want nil", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// Every HTTP server listens on every replica.
//
// controller-runtime starts a runnable that does not say otherwise only
// after the lease is won. Without an explicit answer here the gateway, the
// dashboard and the metrics endpoint would not exist on a standby, so an
// alert reaching it would be refused at the connection rather than answered
// with 503 — which makes a standby indistinguishable from remedik being
// down, the one thing the design rules out.
//
// This was found in an end-to-end run: the leader answered 200 and the
// standby could not be reached at all.
func TestHTTPServerRunsOnEveryReplica(t *testing.T) {
	s := newHTTPServer("gateway", "127.0.0.1:0", http.NotFoundHandler(), quietTestLogger())

	if s.NeedLeaderElection() {
		t.Error("NeedLeaderElection() = true; a standby would have no listener, " +
			"so an alert would be refused at the connection instead of answered with 503")
	}

	// The interface has to be satisfied, not merely the method present: the
	// manager type-switches on it, so a signature drift would silently put
	// this back behind the lease.
	var _ manager.LeaderElectionRunnable = s
}

// The guard replay is the opposite case, and deliberately so.
func TestGuardWarmerWaitsForTheLease(t *testing.T) {
	w := &guardWarmer{}

	if !w.NeedLeaderElection() {
		t.Error("NeedLeaderElection() = false; a standby would replay the guard " +
			"history at boot and enforce cooldowns that were stale by the time it took over")
	}

	var _ manager.LeaderElectionRunnable = w
}
