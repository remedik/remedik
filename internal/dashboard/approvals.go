package dashboard

// The approvals queue.
//
// AwaitingApproval is the only state on this operator with a clock. Every
// other one is history: it happened, and the page says what. This one expires,
// and when it expires the remediation fails and escalates — so a queue
// ordered by age, which is what the list does, puts the record with fourteen
// minutes left above the one with forty seconds.
//
// The page prints the commands that decide a record. It does not send them:
// an approve button needs an identity model the dashboard does not have, and
// an audit trail that cannot say who asked is worse than none. What a page
// that cannot act can still do is put the whole decision in front of the
// person making it — what would run, against what, and how long is left.

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

// approvalsPath is the queue.
const approvalsPath = "/approvals"

// Urgency thresholds. They are about what a reader should do differently, not
// about arithmetic: a minute or less leaves no time to go and read anything
// else, and five or less leaves none to go and ask somebody. Inclusive at both
// bounds, because a record with exactly a minute left is not the calm case.
const (
	approvalCritical = time.Minute
	approvalSoon     = 5 * time.Minute
)

// ApprovalsView is the queue, soonest deadline first.
type ApprovalsView struct {
	Page

	// Queue is every record awaiting a decision, soonest deadline first.
	//
	// Named Queue rather than Waiting because the chrome's own Waiting is the
	// count on the navigation entry, and a page whose field shadows its
	// layout's is a page that renders a badge of nothing.
	Queue []WaitingView
	// Expired counts those whose deadline has passed and which the
	// reconciler has not caught up with.
	Expired int
	// ApprovalStrategies is how many strategies ask for approval at all.
	//
	// It is what makes an empty queue mean something: "nothing is waiting"
	// and "nothing can ever wait, because no strategy asks" are the same
	// empty page and two entirely different situations.
	ApprovalStrategies int
	// Named lists them, so the empty page can point at what to look at.
	Named []string
	// Asking and Fills are the empty page's sentence, either side of those
	// names. They are built here rather than assembled in the template
	// because the verb has to agree with the count and the clause after it
	// changes too: "1 strategy ask for approval ... when one of them matches"
	// is what a plural helper leaves behind when the sentence around it also
	// has to move.
	Asking string
	Fills  string
}

// Any reports whether anything is waiting.
func (v ApprovalsView) Any() bool { return len(v.Queue) > 0 }

// WaitingView is one decision.
type WaitingView struct {
	Name string
	URL  string

	Strategy    string
	StrategyURL string
	Target      string
	TargetURL   string
	Alert       string

	// Age is how long it has been waiting, and Created the exact moment.
	Age     string
	Created string

	// Left is the time remaining, empty once there is none. Expired reports
	// the deadline having passed — shown as expired rather than as a negative
	// number, because the reconcile that fails the record may not have run
	// yet and "minus four minutes" is not a thing a reader can act on.
	Left     string
	Expired  bool
	Deadline string
	// NoDeadline reports a record carrying none at all. The engine refuses to
	// hold one of those — a human gate that waits for ever is the one outcome
	// it must not have — so it is already on its way to ApprovalTimeout, and
	// the page says that rather than implying there is time.
	NoDeadline bool

	// left is what the queue is ordered by: how long is left, negative once
	// the deadline has passed. A record with no deadline is the most urgent
	// there is, because it is already over.
	left time.Duration
	// Tone is the urgency, so a row about to expire does not look like one
	// with an hour left.
	Tone string

	// Steps are what would run if it is approved. Approving something whose
	// effect is on another page is how a person approves the wrong thing at
	// three in the morning.
	Steps []StepView

	// Approve and Deny are the commands, printed to be copied.
	Approve string
	Deny    string
}

func buildApprovals(
	remediations []v1alpha1.Remediation,
	strategies []v1alpha1.RemediationStrategy,
	now time.Time,
) ApprovalsView {
	var view ApprovalsView

	for i := range strategies {
		if strategies[i].Spec.Execution.Mode == v1alpha1.ExecutionModeApproval {
			view.ApprovalStrategies++
			view.Named = append(view.Named, strategies[i].Name)
		}
	}
	sort.Strings(view.Named)
	view.Asking, view.Fills = askingSentence(view.ApprovalStrategies)

	for i := range remediations {
		rem := &remediations[i]
		if rem.Status.State != v1alpha1.RemediationStateAwaitingApproval {
			continue
		}
		waiting := buildWaiting(rem, now)
		if waiting.Expired {
			view.Expired++
		}
		view.Queue = append(view.Queue, waiting)
	}

	// The order the queue will empty itself in. Ties break by name so two
	// records with the same deadline do not swap places between refreshes.
	sort.Slice(view.Queue, func(i, j int) bool {
		a, b := view.Queue[i], view.Queue[j]
		if a.left != b.left {
			return a.left < b.left
		}
		return a.Name < b.Name
	})
	return view
}

// askingSentence is the empty page's explanation, with its verb agreeing.
func askingSentence(strategies int) (asking, fills string) {
	if strategies == 1 {
		return "1 strategy asks for approval",
			"so this queue fills when it matches an alert"
	}
	return fmt.Sprintf("%d strategies ask for approval", strategies),
		"so this queue fills when one of them matches an alert"
}

func buildWaiting(rem *v1alpha1.Remediation, now time.Time) WaitingView {
	view := WaitingView{
		Name:     rem.Name,
		URL:      "/remediations/" + rem.Name,
		Strategy: rem.Spec.StrategyName,
		Target:   rem.Spec.Target,
		Alert:    rem.Spec.Alert.Name,
		Age:      FormatAge(rem.CreationTimestamp.Time, now),
		Created:  FormatTimestamp(rem.CreationTimestamp.Time),
		// Nothing has been resolved yet — that happens when it is approved,
		// against the cluster as it is then — so these are the declared steps
		// rather than a plan.
		Steps: joinSteps(rem.Spec.Steps, nil),
		Tone:  toneWaiting,
	}
	view.Approve, view.Deny = approvalCommands(rem)

	if rem.Spec.StrategyName != "" {
		view.StrategyURL = Filter{Strategy: rem.Spec.StrategyName}.Path()
	}
	if rem.Spec.Target != "" {
		view.TargetURL = Filter{Target: rem.Spec.Target}.Path()
	}

	deadline := rem.Spec.ApprovalDeadline
	if deadline == nil || deadline.IsZero() {
		// Sorted ahead of everything that still has a deadline, because it is
		// not waiting: the next reconcile fails it.
		view.NoDeadline, view.Expired = true, true
		view.Tone = toneFailed
		view.left = math.MinInt64
		return view
	}

	view.Deadline = FormatTimestamp(deadline.Time)
	view.left = deadline.Sub(now)
	switch {
	case view.left <= 0:
		view.Expired = true
		view.Tone = toneFailed
	case view.left <= approvalCritical:
		view.Left = FormatCountdown(view.left)
		view.Tone = toneFailed
	case view.left <= approvalSoon:
		view.Left = FormatCountdown(view.left)
		view.Tone = toneWarn
	default:
		view.Left = FormatCountdown(view.left)
	}
	return view
}

// approvalCommands is the decision, as the two commands that make it.
//
// The namespace and name are the record's own, so the command works when
// pasted rather than after somebody remembers to change it. Both pages that
// print them share this: the queue and the record's own page had drifted into
// two spellings of the same patch once already.
func approvalCommands(rem *v1alpha1.Remediation) (approve, deny string) {
	patch := func(decision string) string {
		return fmt.Sprintf(
			`kubectl -n %s patch remediation %s --type merge \
  -p '{"spec":{"approval":{"decision":"%s","by":"YOUR-NAME"}}}'`,
			rem.Namespace, rem.Name, decision)
	}
	return patch("approve"), patch("deny")
}
