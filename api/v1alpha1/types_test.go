package v1alpha1

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestRemediationState_IsTerminal(t *testing.T) {
	tests := []struct {
		state RemediationState
		want  bool
	}{
		{RemediationStateSucceeded, true},
		{RemediationStateFailed, true},
		{RemediationStateSimulated, true},
		{RemediationStatePending, false},
		{RemediationStateRunning, false},
		{RemediationState(""), false},
		{RemediationState("Bogus"), false},
	}

	for _, tc := range tests {
		t.Run(string(tc.state), func(t *testing.T) {
			if got := tc.state.IsTerminal(); got != tc.want {
				t.Errorf("IsTerminal() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRemediation_IsTerminal(t *testing.T) {
	r := &Remediation{}
	if r.IsTerminal() {
		t.Error("a Remediation with no state reports terminal")
	}

	r.Status.State = RemediationStateSimulated
	if !r.IsTerminal() {
		t.Error("a Simulated Remediation does not report terminal")
	}
}

// An unset `enabled` must mean enabled: the field is a pointer precisely so
// that "not written in the YAML" is distinguishable from "false".
func TestRemediationStrategy_IsEnabled(t *testing.T) {
	truth, lie := true, false

	tests := []struct {
		name    string
		enabled *bool
		want    bool
	}{
		{name: "unset defaults to enabled", enabled: nil, want: true},
		{name: "explicitly true", enabled: &truth, want: true},
		{name: "explicitly false", enabled: &lie, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &RemediationStrategy{}
			s.Spec.Enabled = tc.enabled
			if got := s.IsEnabled(); got != tc.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGroupVersion(t *testing.T) {
	if GroupVersion.Group != "remedik.dev" {
		t.Errorf("Group = %q, want remedik.dev", GroupVersion.Group)
	}
	if GroupVersion.Version != "v1alpha1" {
		t.Errorf("Version = %q, want v1alpha1", GroupVersion.Version)
	}
}

func TestAddToScheme(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme() error = %v, want nil", err)
	}

	for _, kind := range []string{
		"RemediationStrategy", "RemediationStrategyList",
		"Remediation", "RemediationList",
	} {
		gvk := GroupVersion.WithKind(kind)
		if !s.Recognizes(gvk) {
			t.Errorf("scheme does not recognize %s", gvk)
		}
	}
}

func TestResource(t *testing.T) {
	got := Resource("remediations")
	if got.Group != "remedik.dev" || got.Resource != "remediations" {
		t.Errorf("Resource() = %+v, want group remedik.dev / resource remediations", got)
	}
}

// A live record must state its posture rather than leaving it to be inferred
// from an absent field: with per-namespace posture, "was this one simulated?"
// is a real question, asked of a record that exists to explain itself.
func TestRemediationSpec_PostureIsAlwaysSerialized(t *testing.T) {
	encoded, err := json.Marshal(RemediationSpec{StrategyName: "restart-api", DryRun: false})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"dryRun":false`) {
		t.Errorf("a live record serialized as %s, want an explicit \"dryRun\":false", encoded)
	}
}
