package matching

import (
	"testing"

	"github.com/remedik/remedik/internal/alert"
)

// crashLoop is the alert used across these tests.
func crashLoop() alert.Alert {
	return alert.Alert{
		Fingerprint: "f1",
		Status:      alert.StatusFiring,
		Labels: map[string]string{
			"alertname": "KubePodCrashLooping",
			"namespace": "payments",
			"pod":       "api-0",
			"severity":  "warning",
		},
	}
}

func rule(name string, match map[string]string) Rule {
	return Rule{Name: name, Enabled: true, Match: match}
}

func TestMatches(t *testing.T) {
	tests := []struct {
		name string
		rule Rule
		want bool
	}{
		{
			name: "single matching label",
			rule: rule("a", map[string]string{"alertname": "KubePodCrashLooping"}),
			want: true,
		},
		{
			name: "all labels match",
			rule: rule("a", map[string]string{"alertname": "KubePodCrashLooping", "namespace": "payments"}),
			want: true,
		},
		{
			name: "one label differs",
			rule: rule("a", map[string]string{"alertname": "KubePodCrashLooping", "namespace": "checkout"}),
			want: false,
		},
		{
			name: "label absent from alert",
			rule: rule("a", map[string]string{"cluster": "prod-eu"}),
			want: false,
		},
		{
			name: "matching is exact, not a prefix",
			rule: rule("a", map[string]string{"alertname": "KubePod"}),
			want: false,
		},
		{
			name: "matching is case-sensitive",
			rule: rule("a", map[string]string{"alertname": "kubepodcrashlooping"}),
			want: false,
		},
		{
			name: "disabled rule never matches",
			rule: Rule{Name: "a", Enabled: false, Match: map[string]string{"alertname": "KubePodCrashLooping"}},
			want: false,
		},
		{
			name: "rule with no matchers never matches",
			rule: rule("a", map[string]string{}),
			want: false,
		},
		{
			name: "rule with nil matchers never matches",
			rule: rule("a", nil),
			want: false,
		},
		{
			name: "empty expected value must match an empty label",
			rule: rule("a", map[string]string{"team": ""}),
			want: true, // the alert has no "team" label, which reads as ""
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Matches(crashLoop(), tc.rule); got != tc.want {
				t.Errorf("Matches() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSelect_MostSpecificWins(t *testing.T) {
	broad := rule("broad", map[string]string{"alertname": "KubePodCrashLooping"})
	specific := rule("specific", map[string]string{
		"alertname": "KubePodCrashLooping",
		"namespace": "payments",
	})

	// Order of the input must not matter.
	for _, rules := range [][]Rule{{broad, specific}, {specific, broad}} {
		got, ok := Select(crashLoop(), rules)
		if !ok {
			t.Fatal("Select() ok = false, want a match")
		}
		if got.Name != "specific" {
			t.Errorf("Select() = %q, want %q", got.Name, "specific")
		}
	}
}

func TestSelect_TiesBreakOnName(t *testing.T) {
	zebra := rule("zebra", map[string]string{"alertname": "KubePodCrashLooping"})
	alpha := rule("alpha", map[string]string{"namespace": "payments"})

	for _, rules := range [][]Rule{{zebra, alpha}, {alpha, zebra}} {
		got, ok := Select(crashLoop(), rules)
		if !ok {
			t.Fatal("Select() ok = false, want a match")
		}
		if got.Name != "alpha" {
			t.Errorf("Select() = %q, want %q (lexically first on equal specificity)",
				got.Name, "alpha")
		}
	}
}

func TestSelect_NoMatch(t *testing.T) {
	tests := []struct {
		name  string
		rules []Rule
	}{
		{name: "no rules at all", rules: nil},
		{name: "empty rule list", rules: []Rule{}},
		{
			name:  "no rule matches the alert",
			rules: []Rule{rule("other", map[string]string{"alertname": "KubeNodeNotReady"})},
		},
		{
			name: "only matching rule is disabled",
			rules: []Rule{{
				Name:    "disabled",
				Enabled: false,
				Match:   map[string]string{"alertname": "KubePodCrashLooping"},
			}},
		},
		{
			name:  "match-all rule is refused",
			rules: []Rule{rule("catch-all", map[string]string{})},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Select(crashLoop(), tc.rules)
			if ok {
				t.Errorf("Select() ok = true (matched %q), want no match", got.Name)
			}
			if got.Name != "" {
				t.Errorf("Select() returned %+v, want the zero Rule", got)
			}
		})
	}
}

func TestCandidates_OrderedByPrecedence(t *testing.T) {
	rules := []Rule{
		rule("zebra", map[string]string{"alertname": "KubePodCrashLooping"}),
		rule("most", map[string]string{
			"alertname": "KubePodCrashLooping",
			"namespace": "payments",
			"severity":  "warning",
		}),
		rule("alpha", map[string]string{"alertname": "KubePodCrashLooping"}),
		rule("nomatch", map[string]string{"alertname": "Other"}),
		rule("middle", map[string]string{"alertname": "KubePodCrashLooping", "pod": "api-0"}),
	}

	got := Candidates(crashLoop(), rules)

	want := []string{"most", "middle", "alpha", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates (%v), want %d", len(got), names(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("candidate[%d] = %q, want %q (full order: %v)", i, got[i].Name, name, names(got))
		}
	}
}

func TestCandidates_DoesNotMutateInput(t *testing.T) {
	rules := []Rule{
		rule("zebra", map[string]string{"alertname": "KubePodCrashLooping"}),
		rule("alpha", map[string]string{"alertname": "KubePodCrashLooping"}),
	}

	_ = Candidates(crashLoop(), rules)

	if rules[0].Name != "zebra" || rules[1].Name != "alpha" {
		t.Errorf("input slice was reordered: %v", names(rules))
	}
}

func TestRule_Specificity(t *testing.T) {
	if got := rule("a", map[string]string{"x": "1", "y": "2"}).Specificity(); got != 2 {
		t.Errorf("Specificity() = %d, want 2", got)
	}
	if got := rule("a", nil).Specificity(); got != 0 {
		t.Errorf("Specificity() = %d, want 0", got)
	}
}

func names(rules []Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.Name
	}
	return out
}
