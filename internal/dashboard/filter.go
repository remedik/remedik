package dashboard

import (
	"net/url"
	"sort"
	"strings"

	"github.com/remedik/remedik/api/v1alpha1"
)

// Query parameters the overview understands. They are GET parameters rather
// than anything stored, which is not a shortcut: the dashboard allowlists GET
// and HEAD before routing, so a filter that needed a write could not exist
// here. It also means a filtered view is a URL somebody can send to a
// colleague during an incident, which is most of the value.
const (
	paramNamespace = "namespace"
	paramStrategy  = "strategy"
	paramState     = "state"
)

// Filter narrows the executions shown.
//
// The zero value shows everything, so a page with no query string behaves
// exactly as it did before filters existed.
type Filter struct {
	// Namespace is the namespace of the object remediated — not remedik's
	// own. That distinction is the whole point: an operator asks "what has
	// been happening in payments?", never "what has been happening in the
	// namespace remedik is installed in".
	Namespace string
	// Strategy narrows to one rule.
	Strategy string
	// State narrows to one outcome, using the same words the records use.
	State string
}

// ParseFilter reads a filter from a query string.
//
// Unknown values are kept rather than rejected. A namespace that has no
// records is a legitimate thing to ask about — the answer is "nothing
// happened there", which is information — and a 400 for a mistyped
// parameter would turn a shareable URL into a trap.
func ParseFilter(query url.Values) Filter {
	return Filter{
		Namespace: strings.TrimSpace(query.Get(paramNamespace)),
		Strategy:  strings.TrimSpace(query.Get(paramStrategy)),
		State:     strings.TrimSpace(query.Get(paramState)),
	}
}

// Active reports whether anything is being narrowed.
func (f Filter) Active() bool {
	return f.Namespace != "" || f.Strategy != "" || f.State != ""
}

// Matches reports whether a record survives the filter.
func (f Filter) Matches(rem *v1alpha1.Remediation) bool {
	if f.Namespace != "" && TargetNamespace(rem.Spec.Target) != f.Namespace {
		return false
	}
	if f.Strategy != "" && rem.Spec.StrategyName != f.Strategy {
		return false
	}
	if f.State != "" && displayState(rem.Status.State) != f.State {
		return false
	}
	return true
}

// Path is the list page carrying this filter, which is what every filter
// control links to. Filtering is navigation: a link has no state between
// being chosen and being submitted, so there is nothing a background refresh
// can destroy and no Apply button to reach before it fires.
func (f Filter) Path() string { return remediationsPath + f.Query() }

// Query renders the filter back into a query string, so links can preserve
// what is already selected while changing one thing.
func (f Filter) Query() string {
	values := url.Values{}
	for key, value := range map[string]string{
		paramNamespace: f.Namespace,
		paramStrategy:  f.Strategy,
		paramState:     f.State,
	} {
		if value != "" {
			values.Set(key, value)
		}
	}
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

// TargetNamespace pulls the namespace out of a "kind/namespace/name" target.
//
// A two-part target is cluster-scoped — a node — and belongs to no namespace;
// so is an empty one, which is what a target that could not be resolved
// leaves behind. Both answer "", and the filter simply never matches them,
// which is the honest result: they are not in any namespace.
func TargetNamespace(target string) string {
	parts := strings.Split(target, "/")
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
}

// FilterOption is one choice, as the link that applies or removes it.
type FilterOption struct {
	// Value is the namespace, strategy or state.
	Value string
	// URL applies this choice on top of the rest of the filter, or removes
	// it when it is already the one in force — so the same control both
	// narrows and widens, and nothing is ever a dead end.
	URL string
	// Selected reports whether this is the value currently in force.
	Selected bool
	// Count is how many records carry this value, ignoring this dimension's
	// own clause. It is the difference between a control you can use and one
	// you have to try: a namespace showing 0 is one you can skip.
	Count int
}

// FilterGroup is one dimension of the filter and the control that offers it.
type FilterGroup struct {
	// Label names the dimension.
	Label string
	// Param is the query parameter it sets.
	Param string
	// AllURL clears just this dimension.
	AllURL string
	// AllSelected reports whether nothing is chosen here.
	AllSelected bool
	// Options are every value present in the records.
	Options []FilterOption
	// AsSelect reports that there are too many values to draw as links, so
	// the dimension renders as a select with keyboard type-ahead instead.
	AsSelect bool
	// QuickPicks are the busiest few, kept as one-click pills beside the
	// select, plus whatever is in force so it can always be undone.
	QuickPicks []FilterOption
	// Keep is the rest of the filter, which the select's form must send back
	// as hidden fields — otherwise choosing a namespace would silently clear
	// the state somebody had already chosen.
	Keep Filter
}

// KeptParams are the other clauses, as the hidden fields a select's form
// needs to preserve them.
func (g FilterGroup) KeptParams() []Label {
	var kept []Label
	for _, pair := range []Label{
		{Key: paramNamespace, Value: g.Keep.Namespace},
		{Key: paramStrategy, Value: g.Keep.Strategy},
		{Key: paramState, Value: g.Keep.State},
	} {
		if pair.Key != g.Param && pair.Value != "" {
			kept = append(kept, pair)
		}
	}
	return kept
}

// FilterOptions are the choices the controls offer.
//
// They are derived from every record, not from the filtered ones, so a
// choice can always be changed or undone. A control whose options shrink as
// you use it is a control you can get stuck in.
type FilterOptions struct {
	Namespaces []string
	Strategies []string
	States     []string
}

// BuildFilterOptions collects the distinct values present in the records.
func BuildFilterOptions(remediations []v1alpha1.Remediation) FilterOptions {
	namespaces := map[string]bool{}
	strategies := map[string]bool{}
	states := map[string]bool{}

	for i := range remediations {
		if ns := TargetNamespace(remediations[i].Spec.Target); ns != "" {
			namespaces[ns] = true
		}
		if name := remediations[i].Spec.StrategyName; name != "" {
			strategies[name] = true
		}
		states[displayState(remediations[i].Status.State)] = true
	}

	return FilterOptions{
		Namespaces: sortedKeys(namespaces),
		Strategies: sortedKeys(strategies),
		States:     sortedKeys(states),
	}
}

// Any reports whether there is anything worth showing a control for. With
// one namespace and one strategy, a filter row is furniture.
func (o FilterOptions) Any() bool {
	return len(o.Namespaces) > 1 || len(o.Strategies) > 1 || len(o.States) > 1
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// pillLimit is where a dimension stops being a row of links and becomes a
// select.
//
// Pills are the best control for a handful — one click, everything visible,
// no menu to open — and the worst for a hundred and fifty, which is a wall
// nobody scans. A select is not a lesser option above that: browsers give it
// keyboard type-ahead, which is exactly the "find my namespace among 150"
// interaction, for no JavaScript.
const pillLimit = 8

// quickPickLimit is how many of the busiest values stay as pills beside a
// select, so the common cases are still one click.
const quickPickLimit = 4

// Groups turns the available values into controls, with the count each
// choice would yield.
//
// The counts ignore the row's own clause, so the namespace row always shows
// every namespace's total under the *other* filters — which is what makes it
// usable for switching rather than only for narrowing.
func (o FilterOptions) Groups(active Filter, remediations []v1alpha1.Remediation) []FilterGroup {
	specs := []struct {
		label  string
		param  string
		values []string
		chosen string
		rest   Filter
		set    func(*Filter, string)
		key    func(*v1alpha1.Remediation) string
	}{
		{
			label: "Namespace", param: paramNamespace, values: o.Namespaces, chosen: active.Namespace,
			rest: Filter{Strategy: active.Strategy, State: active.State},
			set:  func(f *Filter, v string) { f.Namespace = v },
			key:  func(r *v1alpha1.Remediation) string { return TargetNamespace(r.Spec.Target) },
		},
		{
			label: "Strategy", param: paramStrategy, values: o.Strategies, chosen: active.Strategy,
			rest: Filter{Namespace: active.Namespace, State: active.State},
			set:  func(f *Filter, v string) { f.Strategy = v },
			key:  func(r *v1alpha1.Remediation) string { return r.Spec.StrategyName },
		},
		{
			label: "State", param: paramState, values: o.States, chosen: active.State,
			rest: Filter{Namespace: active.Namespace, Strategy: active.Strategy},
			set:  func(f *Filter, v string) { f.State = v },
			key:  func(r *v1alpha1.Remediation) string { return displayState(r.Status.State) },
		},
	}

	groups := make([]FilterGroup, 0, len(specs))
	for _, spec := range specs {
		// One value is not a choice, and a row offering it is furniture.
		if len(spec.values) < 2 {
			continue
		}

		counts := countByValue(remediations, spec.rest, spec.key)
		options := make([]FilterOption, 0, len(spec.values))
		for _, value := range spec.values {
			// Clicking the value already in force removes it, so the same
			// control both narrows and widens and nothing is a dead end.
			target := spec.rest
			if value != spec.chosen {
				spec.set(&target, value)
			}
			options = append(options, FilterOption{
				Value:    value,
				URL:      target.Path(),
				Selected: value == spec.chosen,
				Count:    counts[value],
			})
		}

		group := FilterGroup{
			Label:       spec.label,
			Param:       spec.param,
			AllURL:      spec.rest.Path(),
			AllSelected: spec.chosen == "",
			Options:     options,
			// The form posts the other clauses back as hidden fields, so
			// choosing a namespace does not silently clear the state filter.
			Keep: spec.rest,
		}
		if len(options) > pillLimit {
			group.AsSelect = true
			group.QuickPicks = busiest(options, quickPickLimit, spec.chosen)
		}
		groups = append(groups, group)
	}
	return groups
}

// countByValue tallies every value of one dimension in a single pass.
//
// The obvious version asked "how many match this?" once per option, and each
// answer scanned every record: 195 options over 10,000 records is 1.95
// million comparisons to draw one page. It read as correct and benchmarked
// at 50ms. This is the same arithmetic without the product.
func countByValue(
	remediations []v1alpha1.Remediation,
	rest Filter,
	key func(*v1alpha1.Remediation) string,
) map[string]int {
	counts := make(map[string]int)
	for i := range remediations {
		rem := &remediations[i]
		// Against the other clauses only: this dimension is the one being
		// chosen, so its own clause must not narrow its own counts.
		if !rest.Matches(rem) {
			continue
		}
		if value := key(rem); value != "" {
			counts[value]++
		}
	}
	return counts
}

// busiest picks the values worth keeping as one-click pills beside a select:
// the largest few, plus whatever is currently in force so the reader can
// always see and undo it.
func busiest(options []FilterOption, limit int, chosen string) []FilterOption {
	ranked := make([]FilterOption, len(options))
	copy(ranked, options)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Count > ranked[j].Count })

	picks := make([]FilterOption, 0, limit+1)
	seen := map[string]bool{}
	for _, option := range ranked {
		if len(picks) == limit {
			break
		}
		picks = append(picks, option)
		seen[option.Value] = true
	}

	if chosen != "" && !seen[chosen] {
		for _, option := range options {
			if option.Value == chosen {
				picks = append(picks, option)
				break
			}
		}
	}
	return picks
}
