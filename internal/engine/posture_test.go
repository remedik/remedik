package engine

import (
	"reflect"
	"strings"
	"testing"
)

func TestPosture_DryRunFor(t *testing.T) {
	tests := []struct {
		name      string
		posture   Posture
		namespace string
		want      bool
	}{
		{
			name:    "the zero value simulates",
			posture: Posture{},
			want:    true,
		},
		{
			name:      "an unset default simulates even for a named namespace",
			posture:   Posture{},
			namespace: "payments",
			want:      true,
		},
		{
			name:      "a namespace with no override takes the default",
			posture:   NewPosture(false, map[string]Mode{"prod": ModeDryRun}),
			namespace: "staging",
			want:      false,
		},
		{
			// The reason this feature exists: live where it has been earned,
			// reporting everywhere else.
			name:      "live in one namespace while the default simulates",
			posture:   NewPosture(true, map[string]Mode{"staging": ModeLive}),
			namespace: "staging",
			want:      false,
		},
		{
			name:      "and the rest of that cluster still simulates",
			posture:   NewPosture(true, map[string]Mode{"staging": ModeLive}),
			namespace: "prod",
			want:      true,
		},
		{
			// The other direction: mostly live, one namespace held back.
			name:      "reporting in one namespace while the default acts",
			posture:   NewPosture(false, map[string]Mode{"prod": ModeDryRun}),
			namespace: "prod",
			want:      true,
		},
		{
			// A node or a webhook. Guessing would be worse than the default,
			// and the default ships as dry-run.
			name:      "a target with no namespace takes the default",
			posture:   NewPosture(true, map[string]Mode{"staging": ModeLive}),
			namespace: "",
			want:      true,
		},
		{
			name:      "a targetless action is not made live by an override",
			posture:   NewPosture(true, map[string]Mode{"": ModeLive}),
			namespace: "",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.posture.DryRunFor(tt.namespace); got != tt.want {
				t.Errorf("DryRunFor(%q) = %v, want %v", tt.namespace, got, tt.want)
			}
		})
	}
}

func TestPosture_Mixed(t *testing.T) {
	tests := []struct {
		name    string
		posture Posture
		want    bool
	}{
		{name: "no overrides", posture: NewPosture(true, nil)},
		{
			name:    "an override that agrees with the default is not a mixture",
			posture: NewPosture(true, map[string]Mode{"prod": ModeDryRun}),
		},
		{
			name:    "one live namespace under a simulating default",
			posture: NewPosture(true, map[string]Mode{"staging": ModeLive}),
			want:    true,
		},
		{
			name:    "one simulating namespace under a live default",
			posture: NewPosture(false, map[string]Mode{"prod": ModeDryRun}),
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.posture.Mixed(); got != tt.want {
				t.Errorf("Mixed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPosture_String(t *testing.T) {
	tests := []struct {
		name    string
		posture Posture
		want    string
	}{
		{
			name:    "no overrides says so plainly",
			posture: NewPosture(true, nil),
			want:    "dryRun everywhere",
		},
		{
			name:    "overrides are named, sorted",
			posture: NewPosture(true, map[string]Mode{"staging": ModeLive, "dev": ModeLive}),
			want:    "dryRun by default; live in dev, staging",
		},
		{
			name:    "and in the other direction",
			posture: NewPosture(false, map[string]Mode{"prod": ModeDryRun}),
			want:    "live by default; dryRun in prod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.posture.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParsePosture(t *testing.T) {
	tests := []struct {
		name    string
		pairs   []string
		want    map[string]Mode
		wantErr string
	}{
		{name: "nothing given"},
		{
			name:  "both modes, and whitespace tolerated",
			pairs: []string{"staging=live", " prod = dryRun "},
			want:  map[string]Mode{"staging": ModeLive, "prod": ModeDryRun},
		},
		{
			name:  "the mode is case-insensitive, because dryrun is what people type",
			pairs: []string{"prod=DRYRUN"},
			want:  map[string]Mode{"prod": ModeDryRun},
		},
		{
			// The expensive guess: "true" could plausibly mean either, and
			// the wrong reading modifies somebody's production namespace.
			name:    "a boolean is refused rather than interpreted",
			pairs:   []string{"prod=true"},
			wantErr: "is not a posture",
		},
		{
			name:    "a pair with no mode",
			pairs:   []string{"staging"},
			wantErr: "not in the form",
		},
		{
			name:    "a pair with no namespace",
			pairs:   []string{"=live"},
			wantErr: "not in the form",
		},
		{
			name:    "the same namespace given two postures",
			pairs:   []string{"prod=live", "prod=dryRun"},
			wantErr: "two postures",
		},
		{
			name:  "the same namespace given the same posture twice is fine",
			pairs: []string{"prod=live", "prod=live"},
			want:  map[string]Mode{"prod": ModeLive},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePosture(tt.pairs)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParsePosture() = %v, want an error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePosture() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParsePosture() = %v, want %v", got, tt.want)
			}
		})
	}
}
