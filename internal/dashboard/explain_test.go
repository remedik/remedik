package dashboard

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/remedik/remedik/api/v1alpha1"
)

// A rule per situation, and each one names what it read: an explanation that
// cannot be checked is an opinion.
func TestExplain_EachRuleRecognisesItsSituation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		record   func() v1alpha1.Remediation
		wantSaid string
		wantRead string
	}{
		{
			name: "an action this build does not have",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("typo", 5)
				rem.Status.Reason = v1alpha1.ReasonUnknownAction
				return rem
			},
			wantSaid: "misspelled",
			wantRead: "spec.steps[].action",
		},
		{
			name: "the operator died mid-attempt",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("cut", 5)
				rem.Status.Reason = v1alpha1.ReasonInterrupted
				return rem
			},
			wantSaid: "somewhere in the middle of this plan",
			wantRead: "status.steps[].phase",
		},
		{
			name: "nobody decided in time",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("ignored", 5)
				rem.Status.Reason = v1alpha1.ReasonApprovalTimeout
				return rem
			},
			wantSaid: "silence, not refusal",
			wantRead: "spec.approvalDeadline",
		},
		{
			name: "somebody said no",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("no", 5)
				rem.Status.Reason = v1alpha1.ReasonDenied
				rem.Spec.Approval = &v1alpha1.Approval{
					Decision: v1alpha1.ApprovalDeny, By: "dana", Note: "rolling forward",
				}
				return rem
			},
			wantSaid: "dana",
			wantRead: "spec.approval.by",
		},
		{
			name: "remedik stopped repeating itself",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("enough", 5)
				rem.Status.Reason = v1alpha1.ReasonGaveUp
				return rem
			},
			wantSaid: "it is the decision, not an attempt",
			wantRead: "status.reason",
		},
		{
			name: "a guard refused it",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("cooling", 5)
				rem.Status.Reason = v1alpha1.ReasonGuardRejected
				return rem
			},
			wantSaid: "Nothing in the cluster was touched",
			wantRead: "status.message",
		},
		{
			name: "the object is not there",
			record: func() v1alpha1.Remediation {
				return failedRemediation("gone", 5)
			},
			wantSaid: "does not exist",
			wantRead: "spec.target",
		},
		{
			name: "the identity is not allowed",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("rbac", 5)
				rem.Status.Steps[1].Message =
					`deployments.apps "api" is forbidden: User "system:serviceaccount:x:y" cannot patch`
				return rem
			},
			wantSaid: "named, never inherited",
			wantRead: "status.steps[].message",
		},
		{
			name: "it ran out of time",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("slow", 5)
				rem.Status.Steps[1].Message = "context deadline exceeded"
				return rem
			},
			wantSaid: "may still be running in the cluster",
			wantRead: "status.steps[].startedAt",
		},
		{
			name: "it could not reach something",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("dns", 5)
				rem.Status.Steps[1].Message =
					`Post "http://pagerduty-proxy.oncall.svc:9000/v2/enqueue": dial tcp: no such host`
				return rem
			},
			wantSaid: "not reachable from inside the cluster",
			wantRead: "status.steps[].message",
		},
		{
			name: "something else edited the object",
			record: func() v1alpha1.Remediation {
				rem := failedRemediation("race", 5)
				rem.Status.Steps[1].Message = "the object has been modified; please apply your changes"
				return rem
			},
			wantSaid: "changed under the step",
			wantRead: "status.steps[].message",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rem := tc.record()
			got := explain(&rem, nil)
			if got == nil {
				t.Fatal("no rule recognised it")
			}
			if !strings.Contains(got.Cause, tc.wantSaid) {
				t.Errorf("cause = %q, want it to say %q", got.Cause, tc.wantSaid)
			}
			if !strings.Contains(strings.Join(got.Read, " "), tc.wantRead) {
				t.Errorf("read %v, want it to cite %q", got.Read, tc.wantRead)
			}
		})
	}
}

// The common shape, and the one invisible in the raw error: the alert says one
// namespace and the target names another.
func TestExplain_NamesTheDisagreementBetweenAlertAndTarget(t *testing.T) {
	rem := failedRemediation("mismatch", 5)
	rem.Spec.Target = "deployment/checkout-eu-prod/checkout-api"
	rem.Spec.Alert.Labels = map[string]string{"namespace": "checkout"}

	got := explain(&rem, nil)
	if !strings.Contains(got.Cause, `"checkout"`) ||
		!strings.Contains(got.Cause, `"checkout-eu-prod"`) {
		t.Errorf("cause = %q, want both namespaces named", got.Cause)
	}
	if !strings.Contains(strings.Join(got.Read, " "), "spec.alert.labels.namespace") {
		t.Errorf("read %v, want the label it compared", got.Read)
	}
	// And the command that settles it.
	if got.Next != "kubectl -n checkout-eu-prod get deployment checkout-api" {
		t.Errorf("next = %q", got.Next)
	}
}

// A node is in no namespace, so the command that looks at one must not invent
// a -n flag.
func TestExplain_AClusterScopedTargetGetsAClusterScopedCommand(t *testing.T) {
	rem := failedRemediation("node", 5)
	rem.Spec.Target = "node/worker-3"

	if got := explain(&rem, nil); got.Next != "kubectl get node worker-3" {
		t.Errorf("next = %q, want no namespace flag", got.Next)
	}
}

// When no rule matches, the page says nothing. The raw message is still there,
// and a guess would be worse than silence.
func TestExplain_SaysNothingAboutWhatItDoesNotRecognise(t *testing.T) {
	rem := failedRemediation("strange", 5)
	rem.Status.Steps[1].Message = "the flux capacitor is out of alignment"
	rem.Status.Message = rem.Status.Steps[1].Message

	if got := explain(&rem, nil); got != nil {
		t.Errorf("explained an unrecognised failure: %+v", got)
	}
}

// A record that failed for a precise reason and has failed here four times
// before has both a proximate cause and a bigger problem. They do not compete.
func TestExplain_RepetitionIsAnObservationBesideTheCause(t *testing.T) {
	records := []v1alpha1.Remediation{
		failedRemediation("run-1", 10),
		failedRemediation("run-2", 70),
		failedRemediation("run-3", 130),
	}
	history := buildTargetHistory(&records[0], records, testNow())

	got := explain(&records[0], history)
	if !strings.Contains(got.Cause, "does not exist") {
		t.Errorf("cause = %q, want the proximate reason kept", got.Cause)
	}
	if !strings.Contains(got.Also, "not the fix here") {
		t.Errorf("also = %q, want the pattern stated beside it", got.Also)
	}
}

// A target remedik keeps successfully fixing is not a remediation problem, and
// nothing else on the page says so.
func TestExplain_SuccessThatKeepsBeingNeededIsWorthSaying(t *testing.T) {
	var records []v1alpha1.Remediation
	for i := range 4 {
		records = append(records, succeededRemediation("run-"+string(rune('a'+i)), (i+1)*30))
	}
	history := buildTargetHistory(&records[0], records, testNow())

	got := explain(&records[0], history)
	if got == nil {
		t.Fatal("nothing said about a target remediated four times")
	}
	if got.Cause != "" {
		t.Errorf("a success has a failure cause: %q", got.Cause)
	}
	if !strings.Contains(got.Also, "keeps returning") {
		t.Errorf("also = %q", got.Also)
	}
}

// The explainer is a pure function of a record: same record, same sentence,
// every time.
func TestExplain_IsDeterministic(t *testing.T) {
	rem := failedRemediation("alone", 5)

	first := explain(&rem, nil)
	second := explain(&rem, nil)

	if first.Cause != second.Cause || first.Next != second.Next {
		t.Error("two calls with the same record disagreed")
	}
	if first.Cause == "" {
		t.Error("no cause from a record that has one")
	}
}

// And it is one structurally, which is the claim worth holding: every
// competitor puts a language model here, and the reason this does not is that
// the binary's only outbound connection is the API server. A file that can
// only reach fmt, strings and the API types cannot call anything.
func TestExplain_ReachesNothingButItsOwnArguments(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "explain.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse explain.go: %v", err)
	}

	allowed := map[string]bool{
		`"fmt"`:     true,
		`"strings"`: true,
		`"github.com/remedik/remedik/api/v1alpha1"`: true,
	}
	for _, imported := range file.Imports {
		if !allowed[imported.Path.Value] {
			t.Errorf("explain.go imports %s; the explainer reads a record and nothing "+
				"else -- no client, no clock, no network", imported.Path.Value)
		}
	}
}
