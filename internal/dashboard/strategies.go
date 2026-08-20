package dashboard

// The strategies page: what remedik is allowed to do, and under what guards.
//
// One file per page, matching overview.go, remediations.go and namespaces.go.
// These lived in view.go, which had grown to hold four pages, the filter, the
// sort and ten formatting helpers — nine hundred lines with no single reason
// to change, so every page's code was read past to reach any other's.

import (
	"fmt"
	"sort"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

// StrategiesView is the strategy list.
type StrategiesView struct {
	Page

	Strategies []StrategyView
	Total      int
	Enabled    int
	Disabled   int
	// NotReady is how many name an action this build does not have. They
	// are the ones worth finding on a Tuesday, so the page offers them as
	// a filter rather than leaving them to be spotted while scrolling.
	NotReady int
	// Show is the view in force: "", "not-ready" or "disabled".
	Show string
	// Shown is how many survive it.
	Shown int
}

// Strategy views the page offers.
const (
	// ShowNotReady lists only the strategies remedik could not run.
	ShowNotReady = "not-ready"
	// ShowDisabled lists only the ones that never match an alert.
	ShowDisabled = "disabled"
)

// Filtered reports whether the page is showing a subset.
func (v StrategiesView) Filtered() bool { return v.Show != "" }

// StrategyView is one strategy and what it has done.
type StrategyView struct {
	Name         string
	Enabled      bool
	Mode         string
	Matchers     []Label
	Cooldown     string
	MaxPerHour   string
	Steps        []StepSpecView
	Runs         int64
	LastRun      string
	LastRunExact string
	Age          string
	Succeeded    int
	Failed       int
	Simulated    int
	Recent       []RemediationRow
	// NotReady carries the message of a Ready condition that is false —
	// a strategy referencing an action this build does not implement, for
	// instance. Empty when the strategy is fine or has no condition yet.
	NotReady string
}

// HasGuards reports whether any guard is enforced. Both guards are opt-in,
// so "none" is a real and visible answer rather than a blank cell.
func (s StrategyView) HasGuards() bool { return s.Cooldown != "" || s.MaxPerHour != "" }

// StepSpecView is a declared step, before it has run.
type StepSpecView struct {
	Number int
	Action string
	Params []Label
}

// ErrorView is any page that is not a page: 401, 404, 405, 503.

func buildStrategies(
	strategies []v1alpha1.RemediationStrategy,
	remediations []v1alpha1.Remediation,
	now time.Time,
	show string,
) StrategiesView {
	ordered := newestFirst(remediations)

	// One pass over the records; every strategy then reads its own slice
	// rather than scanning the list again.
	byStrategy := make(map[string][]*v1alpha1.Remediation, len(strategies))
	for _, rem := range ordered {
		byStrategy[rem.Spec.StrategyName] = append(byStrategy[rem.Spec.StrategyName], rem)
	}

	// Strategies are sorted by pointer for the same reason records are: a
	// RemediationStrategy is a large struct, and this reorders the caller's
	// slice, which with a zero-copy list is the manager's own.
	sortedStrategies := make([]*v1alpha1.RemediationStrategy, len(strategies))
	for i := range strategies {
		sortedStrategies[i] = &strategies[i]
	}
	sort.Slice(sortedStrategies, func(i, j int) bool {
		return sortedStrategies[i].Name < sortedStrategies[j].Name
	})

	view := StrategiesView{Total: len(strategies)}
	view.Strategies = make([]StrategyView, 0, len(strategies))

	for _, strategy := range sortedStrategies {
		item := StrategyView{
			Name:         strategy.Name,
			Enabled:      strategy.IsEnabled(),
			Mode:         string(strategy.Spec.Execution.Mode),
			Matchers:     sortedLabels(strategy.Spec.Trigger.Match),
			Runs:         strategy.Status.ExecutionCount,
			LastRun:      FormatAgeOf(strategy.Status.LastExecutionTime, now),
			LastRunExact: FormatTimestampOf(strategy.Status.LastExecutionTime),
			Age:          FormatAge(strategy.CreationTimestamp.Time, now),
			NotReady:     notReadyMessage(strategy.Status.Conditions),
		}
		if item.Mode == "" {
			item.Mode = string(v1alpha1.ExecutionModeAuto)
		}
		if d := strategy.Spec.Guards.Cooldown; d != nil && d.Duration > 0 {
			item.Cooldown = shortDuration(d.Duration)
		}
		if strategy.Spec.Guards.MaxPerHour > 0 {
			item.MaxPerHour = fmt.Sprint(strategy.Spec.Guards.MaxPerHour)
		}

		for j := range strategy.Spec.Steps {
			item.Steps = append(item.Steps, StepSpecView{
				Number: j + 1,
				Action: strategy.Spec.Steps[j].Action,
				Params: sortedLabels(strategy.Spec.Steps[j].With),
			})
		}

		const perStrategyRecent = 5
		for _, rem := range byStrategy[strategy.Name] {
			switch rem.Status.State {
			case v1alpha1.RemediationStateSucceeded:
				item.Succeeded++
			case v1alpha1.RemediationStateFailed:
				item.Failed++
			case v1alpha1.RemediationStateSimulated:
				item.Simulated++
			case v1alpha1.RemediationStatePending, v1alpha1.RemediationStateRunning:
			}
			if len(item.Recent) < perStrategyRecent {
				item.Recent = append(item.Recent, buildRow(rem, now, Filter{}, Sort{}))
			}
		}

		// The status counter is written by the engine and can lag; the
		// records are the ground truth the reader can click through to.
		if item.Runs == 0 {
			item.Runs = int64(len(byStrategy[strategy.Name]))
		}

		if item.Enabled {
			view.Enabled++
		} else {
			view.Disabled++
		}
		if item.NotReady != "" {
			view.NotReady++
		}

		// The counts above are of every strategy, whatever is being shown:
		// a filter that also changed the numbers beside its own control
		// would make the control impossible to reason about.
		if !showsStrategy(show, item) {
			continue
		}
		view.Strategies = append(view.Strategies, item)
	}

	view.Show = show
	view.Shown = len(view.Strategies)
	return view
}

// showsStrategy reports whether one strategy survives the view in force.
func showsStrategy(show string, item StrategyView) bool {
	switch show {
	case ShowNotReady:
		return item.NotReady != ""
	case ShowDisabled:
		return !item.Enabled
	default:
		return true
	}
}
