package v1alpha1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Go constants and the CRD's validation enums are two lists of the same
// thing, written in different files, kept in step by hand.
//
// This exists because they drifted. `AwaitingApproval` was added as a state and
// the kubebuilder marker was not widened, so the API server refused every write
// of it — and nothing noticed until an end-to-end run against a real cluster,
// thirteen minutes at a time. A generated CRD is a file, so the agreement is a
// claim about files and is checked as one.
func TestCRDEnumsCoverEveryConstant(t *testing.T) {
	remediation := readCRD(t, "remedik.dev_remediations.yaml")
	strategy := readCRD(t, "remedik.dev_remediationstrategies.yaml")

	for _, tc := range []struct {
		what  string
		crd   string
		field string
		want  []string
	}{
		{
			what:  "RemediationState",
			crd:   remediation,
			field: "state",
			want: []string{
				string(RemediationStatePending),
				string(RemediationStateAwaitingApproval),
				string(RemediationStateRunning),
				string(RemediationStateSucceeded),
				string(RemediationStateFailed),
				string(RemediationStateSimulated),
			},
		},
		{
			what:  "ApprovalDecision",
			crd:   remediation,
			field: "decision",
			want:  []string{string(ApprovalApprove), string(ApprovalDeny)},
		},
		{
			what:  "ExecutionMode on the record",
			crd:   remediation,
			field: "mode",
			want: []string{
				string(ExecutionModeAuto),
				string(ExecutionModeApproval),
				string(ExecutionModeManual),
			},
		},
		{
			what:  "ExecutionMode on the strategy",
			crd:   strategy,
			field: "mode",
			want: []string{
				string(ExecutionModeAuto),
				string(ExecutionModeApproval),
				string(ExecutionModeManual),
			},
		},
		{
			what:  "StepPhase",
			crd:   remediation,
			field: "phase",
			want: []string{
				string(StepPhasePending),
				string(StepPhaseRunning),
				string(StepPhaseSucceeded),
				string(StepPhaseFailed),
				string(StepPhaseSkipped),
				string(StepPhaseSimulated),
			},
		},
	} {
		t.Run(tc.what, func(t *testing.T) {
			values := enumValuesFor(tc.crd, tc.field)
			if len(values) == 0 {
				t.Fatalf("no enum found for %q in the CRD; the field was renamed "+
					"or lost its validation marker", tc.field)
			}
			for _, want := range tc.want {
				if !values[want] {
					t.Errorf("the CRD's %q enum does not allow %q, so the API "+
						"server will refuse every write of it. Widen the "+
						"kubebuilder marker and run `make manifests`.",
						tc.field, want)
				}
			}
		})
	}
}

// enumValuesFor collects every value listed under any enum belonging to the
// named field, across every place that field appears in the CRD.
//
// Every occurrence, deliberately: `phase` appears on the remediation's own steps
// and on the escalation's, and an enum widened in one and not the other is the
// same defect in a smaller place.
//
// Scanned by indentation rather than matched with a pattern, because Go's
// regexp has no backreferences and "the enum belonging to this field" is a
// statement about nesting. Reading the lines is also what somebody debugging
// this test would do.
func enumValuesFor(crd, field string) map[string]bool {
	values := map[string]bool{}
	lines := strings.Split(crd, "\n")

	for i, line := range lines {
		if strings.TrimSpace(line) != field+":" {
			continue
		}
		fieldIndent := indentOf(line)

		// Walk this field's block, looking for its enum.
		for j := i + 1; j < len(lines); j++ {
			inner := lines[j]
			if strings.TrimSpace(inner) == "" {
				continue
			}
			if indentOf(inner) <= fieldIndent {
				break // out of the field's block
			}
			if strings.TrimSpace(inner) != "enum:" {
				continue
			}
			// A YAML list item sits at the same indentation as its key, not
			// deeper — which is what the first version of this got wrong, and
			// why it found nothing at all rather than finding the wrong thing.
			enumIndent := indentOf(inner)
			for k := j + 1; k < len(lines); k++ {
				item := strings.TrimSpace(lines[k])
				if indentOf(lines[k]) < enumIndent || !strings.HasPrefix(item, "- ") {
					break
				}
				values[strings.Trim(strings.TrimPrefix(item, "- "), `"`)] = true
			}
			break
		}
	}
	return values
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func readCRD(t *testing.T, name string) string {
	t.Helper()

	// From api/v1alpha1 to the chart's generated CRDs.
	path := filepath.Join("..", "..", "charts", "remedik", "crds", name)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// And the reverse: a state the CRD allows but no constant names would be a
// value the API server accepts and this package cannot produce or recognise.
func TestNoStateInTheCRDIsUnknownToTheCode(t *testing.T) {
	known := map[string]bool{}
	for _, s := range []RemediationState{
		RemediationStatePending,
		RemediationStateAwaitingApproval,
		RemediationStateRunning,
		RemediationStateSucceeded,
		RemediationStateFailed,
		RemediationStateSimulated,
	} {
		known[string(s)] = true
	}

	for value := range enumValuesFor(readCRD(t, "remedik.dev_remediations.yaml"), "state") {
		if !known[value] {
			t.Errorf("the CRD allows state %q, which no constant names: the API "+
				"server would accept a value this package cannot recognise", value)
		}
	}
}

// Every state is either terminal or not, and the switch must not fall through
// to a default that guesses. A new state that nobody classified would be
// treated as non-terminal and reconciled for ever.
func TestEveryStateIsClassified(t *testing.T) {
	terminal := map[RemediationState]bool{
		RemediationStateSucceeded: true,
		RemediationStateFailed:    true,
		RemediationStateSimulated: true,
	}
	waiting := map[RemediationState]bool{
		RemediationStatePending:          true,
		RemediationStateAwaitingApproval: true,
		RemediationStateRunning:          true,
	}

	for value := range enumValuesFor(readCRD(t, "remedik.dev_remediations.yaml"), "state") {
		state := RemediationState(value)
		if !terminal[state] && !waiting[state] {
			t.Errorf("state %q is in neither list; whoever added it has not said "+
				"whether more work happens in it", value)
			continue
		}
		if got := state.IsTerminal(); got != terminal[state] {
			t.Errorf("%q.IsTerminal() = %v, want %v", value, got, terminal[state])
		}
	}
}
