package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

// The view models are what the templates render. Building them here, rather
// than reaching into the API types from the templates, keeps two promises:
// the templates hold no logic worth testing, and everything worth testing
// is a pure function from resources to a struct.

// Navigation identifiers, used to mark the current page in the header.
const (
	navNone         = ""
	navOverview     = "overview"
	navRemediations = "remediations"
	navStrategies   = "strategies"
)

// Tones map a state onto the palette. They are names, not colours: the
// stylesheet decides what "ok" looks like in light and in dark, and every
// tone is paired with a word so colour is never the only signal.
const (
	toneOK      = "ok"
	toneFailed  = "failed"
	toneDryRun  = "dryrun"
	toneRunning = "running"
	toneWaiting = "waiting"
	toneMuted   = "muted"
)

// Posture is what remedik is allowed to do, as the pages need to say it.
//
// It is declared here rather than imported from internal/engine for the same
// reason engine.Snapshot is declared there: the dashboard renders, it does
// not depend on the engine, and the conversion happens once in main.
type Posture struct {
	// DryRun is the default: true when a namespace with no override only
	// reports.
	DryRun bool
	// Live and DryRunOnly are the namespaces that differ from the default,
	// sorted.
	Live       []string
	DryRunOnly []string
}

// Mixed reports whether any namespace differs from the default.
//
// A badge reading "Dry-run" over a cluster where two namespaces are live is
// the most misleading thing per-namespace posture could produce, so the
// pages ask this before they say anything about posture at all.
func (p Posture) Mixed() bool { return len(p.Live) > 0 || len(p.DryRunOnly) > 0 }

// Exceptions is the namespaces that differ, whichever way they differ.
func (p Posture) Exceptions() []string {
	if p.DryRun {
		return p.Live
	}
	return p.DryRunOnly
}

// Page carries what every page's chrome needs.
type Page struct {
	// Title is the browser title and the page heading.
	Title string
	// Nav marks the active navigation entry.
	Nav string
	// DryRun reports the operator's default posture.
	DryRun bool
	// Posture is the whole picture, including the namespaces that differ.
	Posture Posture
	// Namespace is where the records being shown live.
	Namespace string
	// Cluster names the cluster, when the operator was given a name. Empty
	// hides the chip entirely rather than showing a placeholder.
	Cluster string
	// Version is the operator build.
	Version string
	// Asset fingerprints the stylesheet and script, so an upgraded operator
	// is never read through a cached copy of the old one.
	Asset string
	// RenderedAt is when this page was produced.
	RenderedAt string
}

// Label is one key/value pair — an alert label, an action parameter or a
// trigger matcher.
type Label struct {
	Key   string
	Value string
}

// Stat is one figure on the overview.
//
// Every figure links to the list it counts. A number somebody cannot click
// is a number they have to go and look for, which on a dashboard is the same
// as not having it.
type Stat struct {
	Label  string
	Value  string
	Detail string
	Tone   string
	URL    string
}

// RemediationRow is one execution in a list.
type RemediationRow struct {
	Name     string
	URL      string
	Strategy string
	Target   string
	Alert    string
	State    string
	Tone     string
	Age      string
	AgeExact string
	Duration string
	Attempt  int32
	DryRun   bool
	Reason   string
	// Escalated is "sent" or "failed" when this remediation ran an
	// escalation, and empty otherwise. It rides beside the state rather
	// than in a column of its own: most rows would have nothing in it, and
	// the one that matters — a failure whose page did not go out — needs to
	// be loud in the list, not discoverable one click away.
	Escalated string
}

// Escalation markers, as the list renders them.
const (
	escalationSent   = "sent"
	escalationFailed = "failed"
)

// DryRunReport is the summary an operator shows their team before turning
// dry-run off: how much would have happened, over how long, and by which
// strategy.
type DryRunReport struct {
	// Active reports whether dry-run is on now. A report can also be shown
	// after it was turned off, describing the trial that led to that.
	Active     bool
	Simulated  int
	Targets    int
	Since      string
	SinceAge   string
	Period     string
	ByStrategy []SimulatedTally
}

// SimulatedTally is one strategy's share of a dry-run trial.
type SimulatedTally struct {
	Strategy string
	Count    int
	Targets  int
	Share    int
	// Example is the plan line from the most recent simulation, which is
	// the literal answer to "what would this have done?".
	Example string
	LastRun string
}

// RemediationView is one execution, in full.
type RemediationView struct {
	Page

	Name     string
	Strategy string
	Target   string
	State    string
	Tone     string
	Summary  string
	Reason   string
	Message  string
	DryRun   bool
	Attempt  int32
	// MaxAttempts states the retry budget the way it reads on the page:
	// one attempt, plus the retries the strategy allowed.
	MaxAttempts int32
	Created     string
	CreatedAge  string
	Started     string
	Completed   string
	Duration    string
	Alert       AlertView
	Steps       []StepView
	// Escalation is who was told, and whether telling them worked. Nil when
	// the strategy declares no escalation — which is itself worth seeing on
	// a failed remediation, so the page says so rather than staying silent.
	Escalation *EscalationView
	// Failed is the terminal state, kept as a bool because the page asks the
	// question more than once and State is a display string.
	Failed bool
}

// EscalationView is the onFailure plan and what became of it.
//
// It is deliberately a separate block on the page. A page that folded these
// into the steps would make "we told PagerDuty" read as a fourth attempt at
// the restart, and would hide the case that matters most: the remediation
// failed and the page failed too, so nobody knows.
type EscalationView struct {
	Phase     string
	Tone      string
	Message   string
	Completed string
	Steps     []StepView
	// Sent reports whether anybody was actually told.
	Sent bool
}

// ShowMessage reports whether the escalation's own message adds anything to
// the steps below it. With one step it is the same sentence twice, and a page
// that repeats itself looks like it is padding.
func (v EscalationView) ShowMessage() bool {
	if v.Message == "" {
		return false
	}
	for _, step := range v.Steps {
		if step.Message == v.Message {
			return false
		}
	}
	return true
}

// NobodyWasTold reports a failed remediation with no escalation declared.
//
// It is not a criticism — most strategies do not need one. It is on the page
// because this is the moment somebody discovers the feature exists, and
// because "it failed and no alert went anywhere" is a fact worth stating out
// loud rather than leaving to be inferred from an absence.
func (v RemediationView) NobodyWasTold() bool { return v.Failed && v.Escalation == nil }

// ShowRawMessage reports whether the status message adds anything to the
// summary. For a failed step the summary already quotes it, and saying the
// same thing twice makes a page look like it is padding.
func (v RemediationView) ShowRawMessage() bool {
	if v.Message == "" {
		return false
	}
	return v.Reason != v1alpha1.ReasonStepFailed && v.Reason != v1alpha1.ReasonUnknownAction
}

// AlertView is the alert that triggered an execution.
type AlertView struct {
	Name        string
	Fingerprint string
	StartsAt    string
	StartsAge   string
	Labels      []Label
}

// StepView is one step of the plan, joined with whatever happened to it.
type StepView struct {
	Number   int
	Action   string
	Target   string
	Phase    string
	Tone     string
	Plan     string
	Message  string
	Params   []Label
	Started  string
	Duration string
	// Kubectl is the equivalent command a human would have typed. Shown so
	// that the change is reviewable by someone who has never read remedik's
	// source — which is most of the people who will read this page.
	Kubectl string
	// Outputs are what the action specifically knew: replicas, an exit
	// code, a revision.
	Outputs []Label
	// Verified is what the action's own post-condition check found. Empty
	// means the action does not check its work, or this was a dry run.
	Verified string
	// Ran reports whether this step has a recorded outcome. A step with
	// none never started, which is a different thing from one that was
	// skipped after an earlier failure.
	Ran bool
}

// StrategiesView is the strategy list.
type StrategiesView struct {
	Page

	Strategies []StrategyView
	Total      int
	Enabled    int
	Disabled   int
}

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
type ErrorView struct {
	Page

	Status int
	Title  string
	Detail string
}

// --------------------------------------------------------------------------
// Builders
// --------------------------------------------------------------------------

// buildDryRunReport summarises the simulated records.
//
// It is built whenever dry-run is on — a trial that has produced nothing
// yet is itself worth stating — and whenever simulations exist, so the
// report an operator based their decision on survives turning dry-run off.
func buildDryRunReport(remediations []v1alpha1.Remediation, dryRun bool, now time.Time) *DryRunReport {
	type tally struct {
		count   int
		targets map[string]struct{}
		example string
		last    time.Time
	}

	byStrategy := map[string]*tally{}
	targets := map[string]struct{}{}
	var simulated int
	var earliest time.Time

	// The list is newest first, so the first record seen for a strategy is
	// its most recent one — which is the example worth showing.
	for i := range remediations {
		rem := &remediations[i]
		if rem.Status.State != v1alpha1.RemediationStateSimulated {
			continue
		}
		simulated++

		created := rem.CreationTimestamp.Time
		if earliest.IsZero() || created.Before(earliest) {
			earliest = created
		}
		if rem.Spec.Target != "" {
			targets[rem.Spec.Target] = struct{}{}
		}

		t, ok := byStrategy[rem.Spec.StrategyName]
		if !ok {
			t = &tally{targets: map[string]struct{}{}, example: firstPlan(rem), last: created}
			byStrategy[rem.Spec.StrategyName] = t
		}
		t.count++
		if rem.Spec.Target != "" {
			t.targets[rem.Spec.Target] = struct{}{}
		}
		if created.After(t.last) {
			t.last = created
		}
	}

	if !dryRun && simulated == 0 {
		return nil
	}

	report := &DryRunReport{
		Active:    dryRun,
		Simulated: simulated,
		Targets:   len(targets),
	}
	if !earliest.IsZero() {
		report.Since = FormatTimestamp(earliest)
		report.SinceAge = FormatAge(earliest, now)
		report.Period = FormatPeriod(now.Sub(earliest))
	}

	for name, t := range byStrategy {
		report.ByStrategy = append(report.ByStrategy, SimulatedTally{
			Strategy: name,
			Count:    t.count,
			Targets:  len(t.targets),
			Share:    percent(t.count, simulated),
			Example:  t.example,
			LastRun:  FormatAge(t.last, now),
		})
	}
	// Busiest first, then by name so the order is stable between refreshes
	// of the same data.
	sort.Slice(report.ByStrategy, func(i, j int) bool {
		if report.ByStrategy[i].Count != report.ByStrategy[j].Count {
			return report.ByStrategy[i].Count > report.ByStrategy[j].Count
		}
		return report.ByStrategy[i].Strategy < report.ByStrategy[j].Strategy
	})

	return report
}

func buildRow(rem *v1alpha1.Remediation, now time.Time) RemediationRow {
	state := displayState(rem.Status.State)
	return RemediationRow{
		Name:      rem.Name,
		URL:       "/remediations/" + rem.Name,
		Strategy:  rem.Spec.StrategyName,
		Target:    rem.Spec.Target,
		Alert:     rem.Spec.Alert.Name,
		State:     state,
		Tone:      stateTone(rem.Status.State),
		Age:       FormatAge(rem.CreationTimestamp.Time, now),
		AgeExact:  FormatTimestamp(rem.CreationTimestamp.Time),
		Duration:  FormatSpan(rem.Status.StartedAt, rem.Status.CompletedAt),
		Attempt:   rem.Status.Attempt,
		DryRun:    rem.Spec.DryRun,
		Reason:    rem.Status.Reason,
		Escalated: escalationMarker(rem.Status.Escalation),
	}
}

func escalationMarker(esc *v1alpha1.EscalationStatus) string {
	switch {
	case esc == nil:
		return ""
	case esc.Phase == v1alpha1.StepPhaseSucceeded:
		return escalationSent
	default:
		return escalationFailed
	}
}

func buildRemediation(rem *v1alpha1.Remediation, now time.Time) RemediationView {
	view := RemediationView{
		Name:        rem.Name,
		Strategy:    rem.Spec.StrategyName,
		Target:      rem.Spec.Target,
		State:       displayState(rem.Status.State),
		Tone:        stateTone(rem.Status.State),
		Reason:      rem.Status.Reason,
		Message:     rem.Status.Message,
		DryRun:      rem.Spec.DryRun,
		Attempt:     rem.Status.Attempt,
		MaxAttempts: rem.Spec.Retries + 1,
		Created:     FormatTimestamp(rem.CreationTimestamp.Time),
		CreatedAge:  FormatAge(rem.CreationTimestamp.Time, now),
		Started:     FormatTimestampOf(rem.Status.StartedAt),
		Completed:   FormatTimestampOf(rem.Status.CompletedAt),
		Duration:    FormatSpan(rem.Status.StartedAt, rem.Status.CompletedAt),
		Alert: AlertView{
			Name:        rem.Spec.Alert.Name,
			Fingerprint: rem.Spec.Alert.Fingerprint,
			StartsAt:    FormatTimestampOf(rem.Spec.Alert.StartsAt),
			StartsAge:   FormatAgeOf(rem.Spec.Alert.StartsAt, now),
			Labels:      sortedLabels(rem.Spec.Alert.Labels),
		},
		Steps: buildSteps(rem),
	}
	view.Failed = rem.Status.State == v1alpha1.RemediationStateFailed
	view.Escalation = buildEscalation(rem)
	view.Summary = summarise(rem, view.Steps)
	return view
}

// applyFilter keeps the records the filter admits, without copying when
// nothing is being narrowed.
func applyFilter(remediations []v1alpha1.Remediation, filter Filter) []v1alpha1.Remediation {
	if !filter.Active() {
		return remediations
	}
	kept := make([]v1alpha1.Remediation, 0, len(remediations))
	for i := range remediations {
		if filter.Matches(&remediations[i]) {
			kept = append(kept, remediations[i])
		}
	}
	return kept
}

// buildSteps joins the plan with what happened to it.
//
// The plan is on the spec and the outcome is on the status, and they can
// disagree in length: a run interrupted after two of three steps has three
// planned and two recorded. Joining by index rather than zipping means a
// step that never started still appears, which is exactly what someone
// reading a failure needs to see.
func buildSteps(rem *v1alpha1.Remediation) []StepView {
	return joinSteps(rem.Spec.Steps, rem.Status.Steps)
}

// joinSteps pairs a plan with its outcome. It serves the remediation's own
// steps and the escalation's alike, because they are the same join.
func joinSteps(plan []v1alpha1.Step, recorded []v1alpha1.StepStatus) []StepView {
	status := make(map[int32]*v1alpha1.StepStatus, len(recorded))
	highest := -1
	for i := range recorded {
		st := &recorded[i]
		status[st.Index] = st
		if int(st.Index) > highest {
			highest = int(st.Index)
		}
	}

	count := max(len(plan), highest+1)
	steps := make([]StepView, 0, count)

	for i := range count {
		view := StepView{Number: i + 1, Phase: string(v1alpha1.StepPhasePending)}

		if i < len(plan) {
			view.Action = plan[i].Action
			view.Params = sortedLabels(plan[i].With)
		}

		if st, ok := status[int32(i)]; ok {
			view.Ran = true
			if st.Action != "" {
				view.Action = st.Action
			}
			view.Target = st.Target
			view.Phase = string(st.Phase)
			view.Plan = st.Plan
			view.Message = st.Message
			view.Kubectl = st.Kubectl
			view.Outputs = sortedLabels(st.Outputs)
			view.Verified = st.Verified
			view.Started = FormatTimestampOf(st.StartedAt)
			view.Duration = FormatSpan(st.StartedAt, st.CompletedAt)
		}

		view.Tone = phaseTone(v1alpha1.StepPhase(view.Phase))
		steps = append(steps, view)
	}

	return steps
}

// buildEscalation renders the onFailure plan's outcome, when there was one.
func buildEscalation(rem *v1alpha1.Remediation) *EscalationView {
	esc := rem.Status.Escalation
	if esc == nil {
		return nil
	}

	sent := esc.Phase == v1alpha1.StepPhaseSucceeded
	return &EscalationView{
		Phase:     string(esc.Phase),
		Tone:      phaseTone(esc.Phase),
		Message:   esc.Message,
		Completed: FormatTimestampOf(esc.CompletedAt),
		Steps:     joinSteps(rem.Spec.EscalationSteps, esc.Steps),
		Sent:      sent,
	}
}

// summarise writes the one line that answers "so what happened?" without
// making the reader assemble it from the fields below.
func summarise(rem *v1alpha1.Remediation, steps []StepView) string {
	switch rem.Status.State {
	case v1alpha1.RemediationStateSucceeded:
		return fmt.Sprintf("Completed %s.", plural(len(steps), "step"))

	case v1alpha1.RemediationStateSimulated:
		return "Dry-run: the plan below was recorded and nothing in the cluster was changed."

	case v1alpha1.RemediationStateFailed:
		switch rem.Status.Reason {
		case v1alpha1.ReasonInterrupted:
			return "The operator restarted while this attempt was running. It was failed rather " +
				"than resumed, because silently repeating a step that had already changed " +
				"something is the worse outcome."
		case v1alpha1.ReasonUnknownAction:
			return "A step named an action this build does not implement. " + rem.Status.Message
		case v1alpha1.ReasonStepFailed:
			if step, ok := failedStep(steps); ok {
				return fmt.Sprintf("Step %d (%s) failed: %s", step.Number, step.Action, step.Message)
			}
			return "A step failed and no retries remained. " + rem.Status.Message
		default:
			if rem.Status.Message != "" {
				return rem.Status.Message
			}
			return "The execution failed."
		}

	case v1alpha1.RemediationStateRunning:
		return fmt.Sprintf("Attempt %d is running.", rem.Status.Attempt)

	case v1alpha1.RemediationStatePending:
		if rem.Status.Attempt > 0 {
			return fmt.Sprintf("Attempt %d failed; waiting to retry (%s allowed).",
				rem.Status.Attempt, plural(int(rem.Spec.Retries), "retry-retries"))
		}
		return "Created and waiting for the reconciler to pick it up."

	default:
		return "Created and waiting for the reconciler to pick it up."
	}
}

func failedStep(steps []StepView) (StepView, bool) {
	for _, step := range steps {
		if step.Phase == string(v1alpha1.StepPhaseFailed) {
			return step, true
		}
	}
	return StepView{}, false
}

func buildStrategies(
	strategies []v1alpha1.RemediationStrategy,
	remediations []v1alpha1.Remediation,
	now time.Time,
) StrategiesView {
	sortNewestFirst(remediations)

	// One pass over the records; every strategy then reads its own slice
	// rather than scanning the list again.
	byStrategy := map[string][]*v1alpha1.Remediation{}
	for i := range remediations {
		name := remediations[i].Spec.StrategyName
		byStrategy[name] = append(byStrategy[name], &remediations[i])
	}

	sort.Slice(strategies, func(i, j int) bool { return strategies[i].Name < strategies[j].Name })

	view := StrategiesView{Total: len(strategies)}
	view.Strategies = make([]StrategyView, 0, len(strategies))

	for i := range strategies {
		strategy := &strategies[i]
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
				item.Recent = append(item.Recent, buildRow(rem, now))
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
		view.Strategies = append(view.Strategies, item)
	}

	return view
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// sortNewestFirst orders records the way an incident is read: what just
// happened, first. Ties break by name so two records created in the same
// second do not swap places between refreshes.
func sortNewestFirst(remediations []v1alpha1.Remediation) {
	sort.Slice(remediations, func(i, j int) bool {
		a, b := remediations[i].CreationTimestamp.Time, remediations[j].CreationTimestamp.Time
		if a.Equal(b) {
			return remediations[i].Name < remediations[j].Name
		}
		return a.After(b)
	})
}

// sortedLabels renders a map as a stable, sorted list. Map iteration order
// is random in Go, and a page whose rows move on every refresh is a page
// nobody trusts.
func sortedLabels(m map[string]string) []Label {
	if len(m) == 0 {
		return nil
	}
	labels := make([]Label, 0, len(m))
	for k, v := range m {
		labels = append(labels, Label{Key: k, Value: v})
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].Key < labels[j].Key })
	return labels
}

// displayState names the state a record is in. An empty state is a record
// the reconciler has not reached yet, which reads as Pending.
func displayState(state v1alpha1.RemediationState) string {
	if state == "" {
		return string(v1alpha1.RemediationStatePending)
	}
	return string(state)
}

func stateTone(state v1alpha1.RemediationState) string {
	switch state {
	case v1alpha1.RemediationStateSucceeded:
		return toneOK
	case v1alpha1.RemediationStateFailed:
		return toneFailed
	case v1alpha1.RemediationStateSimulated:
		return toneDryRun
	case v1alpha1.RemediationStateRunning:
		return toneRunning
	case v1alpha1.RemediationStatePending:
		return toneWaiting
	default:
		return toneWaiting
	}
}

func phaseTone(phase v1alpha1.StepPhase) string {
	switch phase {
	case v1alpha1.StepPhaseSucceeded:
		return toneOK
	case v1alpha1.StepPhaseFailed:
		return toneFailed
	case v1alpha1.StepPhaseSimulated:
		return toneDryRun
	case v1alpha1.StepPhaseRunning:
		return toneRunning
	case v1alpha1.StepPhaseSkipped:
		return toneMuted
	case v1alpha1.StepPhasePending:
		return toneWaiting
	default:
		return toneWaiting
	}
}

// notReadyMessage returns the message of a Ready condition that is false.
// A strategy that cannot run is worth saying so on the page that lists it.
func notReadyMessage(conditions []metav1.Condition) string {
	for i := range conditions {
		if conditions[i].Type != "Ready" || conditions[i].Status != metav1.ConditionFalse {
			continue
		}
		if msg := conditions[i].Message; msg != "" {
			return msg
		}
		return conditions[i].Reason
	}
	return ""
}

// firstPlan is the plan line of a record's first recorded step — the
// sentence that says what would have been done.
func firstPlan(rem *v1alpha1.Remediation) string {
	for i := range rem.Status.Steps {
		if plan := rem.Status.Steps[i].Plan; plan != "" {
			return plan
		}
	}
	return ""
}

func successRate(succeeded, failed int) string {
	total := succeeded + failed
	if total == 0 {
		return "nothing executed yet"
	}
	return fmt.Sprintf("%d%% of executed runs", percent(succeeded, total))
}

func failedDetail(failed int) string {
	if failed == 0 {
		return "none"
	}
	return "needs a look"
}

func percent(part, total int) int {
	if total == 0 {
		return 0
	}
	return int(float64(part)/float64(total)*100 + 0.5)
}

// shortDuration renders a guard's duration the way it was written in the
// manifest — "15m", not "15m0s".
func shortDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	default:
		return d.String()
	}
}

// unit is plural's other half: the noun alone, for places that already show
// the number. "1 1 escalation failed" is what happens without it.
func unit(n int, name string) string {
	singular, pluralForm, irregular := strings.Cut(name, "-")
	if !irregular {
		pluralForm = singular + "s"
	}
	if n == 1 {
		return singular
	}
	return pluralForm
}

// plural handles the units this package counts. Irregular plurals are given
// explicitly as "singular-plural"; everything else takes an s.
func plural(n int, unit string) string {
	singular, pluralForm, irregular := strings.Cut(unit, "-")
	if !irregular {
		pluralForm = singular + "s"
	}
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, pluralForm)
}
