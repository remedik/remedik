package matching

import (
	"testing"

	"github.com/remedik/remedik/internal/alert"
)

// "no strategy matches this alert" is true and useless when a strategy was
// meant to. Every reason a rule can fail has to name the label, because the
// mistake is nearly always a label.
func TestWhyNot(t *testing.T) {
	a := alert.Alert{Labels: map[string]string{
		"alertname": "KubePodCrashLooping",
		"namespace": "payments",
	}}

	for _, tc := range []struct {
		name string
		rule Rule
		want string
	}{
		{
			name: "it does match",
			rule: Rule{Name: "r", Enabled: true, Match: map[string]string{
				"alertname": "KubePodCrashLooping"}},
			want: "",
		},
		{
			name: "disabled beats every other reason",
			rule: Rule{Name: "r", Match: map[string]string{"alertname": "nope"}},
			want: "disabled",
		},
		{
			name: "no matchers is refused rather than matching everything",
			rule: Rule{Name: "r", Enabled: true},
			want: "no matchers, which would match every alert, so it is refused",
		},
		{
			name: "a label the alert does not carry at all",
			rule: Rule{Name: "r", Enabled: true, Match: map[string]string{
				"exported_namespace": "payments"}},
			want: "the alert has no exported_namespace label; the strategy wants " +
				"exported_namespace=payments",
		},
		{
			name: "a label whose value differs",
			rule: Rule{Name: "r", Enabled: true, Match: map[string]string{
				"namespace": "checkout"}},
			want: "namespace is payments, the strategy wants checkout",
		},
		{
			name: "a value with whitespace, which looks identical in YAML",
			rule: Rule{Name: "r", Enabled: true, Match: map[string]string{
				"namespace": "payments "}},
			want: "namespace is payments, the strategy wants payments ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WhyNot(a, tc.rule); got != tc.want {
				t.Errorf("WhyNot() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The explanation must not depend on map iteration order: two runs of the same
// input that read differently are two answers nobody trusts.
func TestWhyNot_IsTheSameEveryTime(t *testing.T) {
	a := alert.Alert{Labels: map[string]string{"alertname": "X"}}
	rule := Rule{Name: "r", Enabled: true, Match: map[string]string{
		"severity": "critical", "namespace": "payments", "cluster": "eu",
	}}

	first := WhyNot(a, rule)
	for range 200 {
		if got := WhyNot(a, rule); got != first {
			t.Fatalf("WhyNot() varies between calls: %q then %q", first, got)
		}
	}
	if first == "" {
		t.Fatal("expected an explanation")
	}
}
