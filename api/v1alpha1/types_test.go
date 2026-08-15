package v1alpha1

import "testing"

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
