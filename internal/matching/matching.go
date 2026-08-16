// Package matching selects which remediation strategy, if any, should
// handle an alert.
//
// The rules are deliberately simple, because an operator woken at 3am has
// to be able to predict them:
//
//   - a strategy matches when every one of its matchers equals the
//     corresponding alert label (equality only, no regex);
//   - at most one strategy runs per alert — the most specific one, meaning
//     the one with the most matchers;
//   - ties are broken by lexical name order, so the outcome never depends
//     on map iteration or apply order.
//
// Like the alert package, this one depends on the standard library only:
// the decision that matters most is the one that must be easiest to test.
package matching

import (
	"sort"

	"github.com/remedik/remedik/internal/alert"
)

// Rule is the subset of a RemediationStrategy needed to choose a handler
// for an alert. The engine builds it from the custom resource.
type Rule struct {
	// Name is the strategy's name; it breaks ties and appears in audit.
	Name string
	// Enabled mirrors spec.enabled. Disabled rules never match.
	Enabled bool
	// Match holds label equality matchers from spec.trigger.match. A rule
	// with no matchers is rejected rather than treated as "match all".
	Match map[string]string
}

// Specificity is the number of matchers a rule requires. Higher wins.
func (r Rule) Specificity() int { return len(r.Match) }

// usable reports whether a rule may take part in selection at all.
//
// A rule with no matchers would match every alert, turning one
// misconfiguration into cluster-wide remediation. The CRD schema rejects
// that shape, and this is the second line of defense: such rules are
// skipped, never applied.
func (r Rule) usable() bool { return r.Enabled && len(r.Match) > 0 }

// Matches reports whether every matcher in r equals the alert's
// corresponding label. Unusable rules never match.
func Matches(a alert.Alert, r Rule) bool {
	if !r.usable() {
		return false
	}
	for key, want := range r.Match {
		if a.Labels[key] != want {
			return false
		}
	}
	return true
}

// Candidates returns every rule matching a, ordered by precedence: most
// specific first, then by name. The engine uses the first element; the
// full list explains "why did this strategy run and not that one".
func Candidates(a alert.Alert, rules []Rule) []Rule {
	matched := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if Matches(a, r) {
			matched = append(matched, r)
		}
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if si, sj := matched[i].Specificity(), matched[j].Specificity(); si != sj {
			return si > sj
		}
		return matched[i].Name < matched[j].Name
	})

	return matched
}

// Select returns the single rule that should handle a. The boolean reports
// whether any rule matched.
func Select(a alert.Alert, rules []Rule) (Rule, bool) {
	candidates := Candidates(a, rules)
	if len(candidates) == 0 {
		return Rule{}, false
	}
	return candidates[0], true
}
