package dashboard

// Why it failed, said once, by a rule that shows its work.
//
// The page quoted the error the action returned — `deployments.apps
// "checkout-api" not found` — which names the symptom and not the cause. Every
// competitor in this category puts a language model here. This does not, for
// two reasons that are the same reason: a sentence generated from a model
// cannot be checked by the person it is talking to, and it would put an
// outbound connection into a binary whose only one is the API server, which is
// a documented property of installing this thing.
//
// So it is a table of rules over fields the record already carries. Each one
// names what it read, so a reader can disagree with it; each one is a pure
// function of the record, so a test can pin it; and when no rule recognises a
// record, the page says nothing rather than guessing. The raw message stays
// where it was, in full, always.

import (
	"fmt"
	"strings"

	"github.com/remedik/remedik/api/v1alpha1"
)

// Explanation is what a rule concluded.
type Explanation struct {
	// Cause is the sentence: what actually went wrong.
	Cause string
	// Also is an observation about this target's history, when there is one
	// worth making. It is separate from Cause because it does not compete
	// with it: a repeated failure still has a proximate cause.
	Also string
	// Next is a command that answers the obvious next question, and NextWhy
	// says what it answers. Both empty when there is nothing useful to run.
	Next    string
	NextWhy string
	// Read names the fields the rule looked at, in the spelling somebody
	// would use to look at them. An explanation that cannot be checked is an
	// opinion.
	Read []string
	Tone string
}

// rule is one recognised situation.
type rule struct {
	// when reports whether this rule applies, and explain says what it means.
	when    func(*v1alpha1.Remediation) bool
	explain func(*v1alpha1.Remediation) Explanation
}

// explain runs the rules in order and returns the first that matches.
//
// history is used only for the supplementary observation, and may be nil.
func explain(rem *v1alpha1.Remediation, history *TargetHistory) *Explanation {
	for _, r := range rules() {
		if !r.when(rem) {
			continue
		}
		found := r.explain(rem)
		found.Also = repeatedly(history)
		return &found
	}

	// Nothing recognised it. A record whose history says something is still
	// worth an observation; a record with neither gets no panel at all.
	if also := repeatedly(history); also != "" {
		return &Explanation{
			Also: also,
			Read: []string{"the other records for this target"},
			Tone: toneWarn,
		}
	}
	return nil
}

func rules() []rule {
	return []rule{
		{reason(v1alpha1.ReasonUnknownAction), unknownAction},
		{reason(v1alpha1.ReasonInterrupted), interrupted},
		{reason(v1alpha1.ReasonApprovalTimeout), approvalTimedOut},
		{reason(v1alpha1.ReasonDenied), denied},
		{reason(v1alpha1.ReasonGaveUp), gaveUp},
		{reason(v1alpha1.ReasonGuardRejected), guardRejected},

		// The message rules, most specific first. They read text another
		// project controls, which is a heuristic — so they add beside the raw
		// message and never replace it.
		{stepFailedWith("not found"), notFound},
		{stepFailedWith("forbidden"), forbidden},
		{stepFailedWith("already exists"), alreadyExists},
		{stepFailedWithAny("context deadline exceeded", "timeout", "timed out"), tooSlow},
		{stepFailedWithAny("no such host", "connection refused", "dial tcp"), unreachable},
		{stepFailedWithAny("conflict", "the object has been modified"), conflicted},
	}
}

// reason matches a record's terminal reason exactly.
func reason(want string) func(*v1alpha1.Remediation) bool {
	return func(rem *v1alpha1.Remediation) bool { return rem.Status.Reason == want }
}

// stepFailedWith matches a failed step whose message contains a phrase.
func stepFailedWith(phrase string) func(*v1alpha1.Remediation) bool {
	return stepFailedWithAny(phrase)
}

func stepFailedWithAny(phrases ...string) func(*v1alpha1.Remediation) bool {
	return func(rem *v1alpha1.Remediation) bool {
		if rem.Status.Reason != v1alpha1.ReasonStepFailed {
			return false
		}
		message := strings.ToLower(failureMessage(rem))
		for _, phrase := range phrases {
			if strings.Contains(message, phrase) {
				return true
			}
		}
		return false
	}
}

// failureMessage is what the failing step said, or the record's own message
// when no step recorded one.
func failureMessage(rem *v1alpha1.Remediation) string {
	for i := range rem.Status.Steps {
		if rem.Status.Steps[i].Phase == v1alpha1.StepPhaseFailed &&
			rem.Status.Steps[i].Message != "" {
			return rem.Status.Steps[i].Message
		}
	}
	return rem.Status.Message
}

// --------------------------------------------------------------------------
// The rules
// --------------------------------------------------------------------------

func unknownAction(_ *v1alpha1.Remediation) Explanation {
	return Explanation{
		Cause: "The strategy names an action this build does not have. That is one " +
			"of two things and they need different fixes: the name is misspelled, " +
			"or the action is real and the chart does not enable it.",
		Next:    "kubectl get remediationstrategies",
		NextWhy: "the READY column names the step and lists what is enabled",
		Read:    []string{"status.reason", "spec.steps[].action"},
		Tone:    toneFailed,
	}
}

func interrupted(_ *v1alpha1.Remediation) Explanation {
	return Explanation{
		Cause: "The operator restarted while this attempt was running. It was failed " +
			"rather than resumed, so any step above marked Succeeded did happen and " +
			"anything after it did not — the cluster is somewhere in the middle of " +
			"this plan.",
		Read: []string{"status.reason", "status.steps[].phase"},
		Tone: toneFailed,
	}
}

func approvalTimedOut(rem *v1alpha1.Remediation) Explanation {
	cause := "Nobody decided in time, so it expired. That is silence, not refusal: " +
		"the difference matters, because a denial would have said why."
	if rem.Status.Escalation != nil && rem.Status.Escalation.Phase == v1alpha1.StepPhaseSucceeded {
		cause += " The escalation went out, so somebody was told the alert is still true."
	}
	return Explanation{
		Cause:   cause,
		Next:    "kubectl get remediationstrategy " + rem.Spec.StrategyName + " -o yaml",
		NextWhy: "execution.approvalTimeout is the window nobody made it inside",
		Read:    []string{"status.reason", "spec.approvalDeadline"},
		Tone:    toneWarn,
	}
}

func denied(rem *v1alpha1.Remediation) Explanation {
	who := "Somebody"
	if rem.Spec.Approval != nil && rem.Spec.Approval.By != "" {
		who = rem.Spec.Approval.By
	}
	cause := who + " looked at this and said no, so nothing ran and nothing was escalated — " +
		"telling them again is not information."
	if rem.Spec.Approval != nil && rem.Spec.Approval.Note != "" {
		cause += " They left a note: " + rem.Spec.Approval.Note
	}
	return Explanation{
		Cause: cause,
		Read:  []string{"spec.approval.decision", "spec.approval.by"},
		Tone:  toneMuted,
	}
}

func gaveUp(_ *v1alpha1.Remediation) Explanation {
	return Explanation{
		Cause: "remedik remediated this target enough times inside the strategy's " +
			"giveUpAfter window without the problem going away, so it stopped. " +
			"This record performed nothing: it is the decision, not an attempt.",
		Read: []string{"status.reason", "spec.target"},
		Tone: toneWarn,
	}
}

func guardRejected(rem *v1alpha1.Remediation) Explanation {
	return Explanation{
		Cause: "A guard refused this before anything ran — a cooldown that had not " +
			"elapsed, an hourly limit already reached, or a blast radius wider than " +
			"the strategy allows. Nothing in the cluster was touched.",
		Next:    "kubectl get remediationstrategy " + rem.Spec.StrategyName + " -o yaml",
		NextWhy: "the guards block is what refused it",
		Read:    []string{"status.reason", "status.message"},
		Tone:    toneMuted,
	}
}

func notFound(rem *v1alpha1.Remediation) Explanation {
	explanation := Explanation{
		Cause: "The object the step was told to act on does not exist. Either it has " +
			"been deleted since the alert fired — a workload that was replaced, or " +
			"scaled away — or it never existed under that name.",
		Read: []string{"spec.target", "status.steps[].message"},
		Tone: toneFailed,
	}

	// The precise version, when the labels disagree with the target. This is
	// the common shape and it is invisible in the raw error: the alert says
	// one namespace and the target names another, usually because the alert
	// carries `exported_namespace` and the strategy expects `namespace`.
	if claimed := rem.Spec.Alert.Labels["namespace"]; claimed != "" {
		if actual := TargetNamespace(rem.Spec.Target); actual != "" && claimed != actual {
			explanation.Cause = fmt.Sprintf(
				"The alert says namespace %q and the step looked in %q. The target was "+
					"built from labels that disagree — which is what a managed rule "+
					"package producing `exported_namespace` looks like from here.",
				claimed, actual)
			explanation.Read = []string{
				"spec.alert.labels.namespace", "spec.target", "status.steps[].message",
			}
		}
	}

	if command, why := lookAtTarget(rem.Spec.Target); command != "" {
		explanation.Next, explanation.NextWhy = command, why
	}
	return explanation
}

func forbidden(_ *v1alpha1.Remediation) Explanation {
	return Explanation{
		Cause: "The identity the step runs as is not allowed to do this. An action's " +
			"authority is named, never inherited: a remediation Job runs as the " +
			"ServiceAccount its step names — remedik's own is refused on purpose — " +
			"so the missing permission belongs to that account, not to the operator.",
		Next:    "kubectl auth can-i --list --as=system:serviceaccount:NAMESPACE:NAME",
		NextWhy: "what the account the step names is actually allowed to do",
		Read:    []string{"status.steps[].message", "spec.steps[].with.serviceAccountName"},
		Tone:    toneFailed,
	}
}

func alreadyExists(_ *v1alpha1.Remediation) Explanation {
	return Explanation{
		Cause: "The step tried to create something that is already there. Usually an " +
			"earlier attempt of this same remediation got that far before failing, " +
			"so the retry met its own leftovers.",
		Read: []string{"status.attempt", "status.steps[].message"},
		Tone: toneFailed,
	}
}

func tooSlow(_ *v1alpha1.Remediation) Explanation {
	return Explanation{
		Cause: "The step did not finish inside the time it was given. What it started " +
			"may still be running in the cluster: a timeout stops remedik waiting, " +
			"it does not undo what was already asked for.",
		Read: []string{"status.steps[].message", "status.steps[].startedAt"},
		Tone: toneFailed,
	}
}

func unreachable(_ *v1alpha1.Remediation) Explanation {
	return Explanation{
		Cause: "Something the step had to reach was not reachable from inside the " +
			"cluster — a name that does not resolve, or a port with nothing on it. " +
			"The remediation is fine; the thing it was talking to is not.",
		Read: []string{"status.steps[].message"},
		Tone: toneFailed,
	}
}

func conflicted(_ *v1alpha1.Remediation) Explanation {
	return Explanation{
		Cause: "The object changed under the step between reading it and writing it. " +
			"Something else is editing the same workload — another controller, a " +
			"deploy, or a person.",
		Read: []string{"status.steps[].message"},
		Tone: toneFailed,
	}
}

// --------------------------------------------------------------------------
// The observation
// --------------------------------------------------------------------------

// repeatedFloor is how many records for one target make a pattern rather than
// a coincidence.
const repeatedFloor = 3

// repeatedly is the sentence about this target's history, when there is one.
//
// It is separate from the cause because it does not compete with it: a
// remediation that failed for a precise reason and has failed here four times
// before has both a proximate cause and a bigger problem.
func repeatedly(history *TargetHistory) string {
	if history == nil || len(history.Marks) < repeatedFloor {
		return ""
	}

	var failures, successes int
	for _, mark := range history.Marks {
		switch mark.Tone {
		case toneFailed:
			failures++
		case toneOK:
			successes++
		}
	}
	total := len(history.Marks) + history.More

	switch {
	case failures >= repeatedFloor:
		return fmt.Sprintf(
			"This is not the first time: %d of the last %d remediations for this target "+
				"failed. Remediation is not the fix here — something keeps putting it back.",
			failures, len(history.Marks))
	case successes >= repeatedFloor:
		// Counted rather than characterised: "each one worked" was written on a
		// record that had just failed, because the total included it.
		return fmt.Sprintf(
			"remedik has fixed this target %s, out of the %s it still has records "+
				"for. The problem keeps returning rather than being fixed.",
			plural(successes, "time"), plural(total, "remediation"))
	default:
		return ""
	}
}

// lookAtTarget turns a "kind/namespace/name" target into the command that
// shows whether it is there.
func lookAtTarget(target string) (command, why string) {
	parts := strings.Split(target, "/")
	switch len(parts) {
	case 2:
		// Cluster-scoped: a node is in no namespace.
		return fmt.Sprintf("kubectl get %s %s", parts[0], parts[1]),
			"whether the object is there at all"
	case 3:
		return fmt.Sprintf("kubectl -n %s get %s %s", parts[1], parts[0], parts[2]),
			"whether the object is there at all"
	default:
		return "", ""
	}
}
