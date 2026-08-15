package main

import (
	"log/slog"
	"testing"
)

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
