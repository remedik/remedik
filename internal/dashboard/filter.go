package dashboard

import (
	"net/url"
	"sort"
	"strings"

	"github.com/ratyx/remedik/api/v1alpha1"
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

// FilterOptions are the choices a filter control offers.
//
// They are derived from every record, not from the filtered ones, so a
// selection can always be changed or undone. A control whose options shrink
// as you use it is a control you can get stuck in.
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
