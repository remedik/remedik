package dashboard

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

// A page that cannot write can still say exactly what to type, and the person
// reading a waiting remediation is the person about to go and look for it.
func TestRemediationView_AWaitingRecordCarriesTheCommands(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	deadline := metav1.NewTime(now.Add(30 * time.Minute))

	rem := &v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{Name: "drain-safely-x7k2q", Namespace: "remedik"},
		Spec: v1alpha1.RemediationSpec{
			StrategyName:     "drain-safely",
			Mode:             v1alpha1.ExecutionModeApproval,
			ApprovalDeadline: &deadline,
		},
		Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateAwaitingApproval},
	}

	view := buildRemediation(rem, nil, now)

	if view.Approval == nil {
		t.Fatal("a waiting remediation has no approval commands, so the page " +
			"leaves the reader to go and find them")
	}
	for _, want := range []string{
		// The record's own namespace and name, so it works when pasted.
		"-n remedik", "drain-safely-x7k2q",
		`"decision":"approve"`,
	} {
		if !strings.Contains(view.Approval.Approve, want) {
			t.Errorf("the approve command does not contain %q:\n%s", want, view.Approval.Approve)
		}
	}
	if !strings.Contains(view.Approval.Deny, `"decision":"deny"`) {
		t.Errorf("the deny command does not deny:\n%s", view.Approval.Deny)
	}
	if view.Approval.Left == "" {
		t.Error("no time left is shown, so the reader cannot tell it is about to escalate")
	}

	// And the summary no longer claims it is waiting for the reconciler.
	if strings.Contains(view.Summary, "reconciler") {
		t.Errorf("summary = %q; a record waiting for a person is not waiting "+
			"for the reconciler", view.Summary)
	}
}

// Every other record gets none, so no page invites a decision that is not open.
func TestRemediationView_OnlyAWaitingRecordCarriesTheCommands(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	for _, state := range []v1alpha1.RemediationState{
		v1alpha1.RemediationStatePending,
		v1alpha1.RemediationStateRunning,
		v1alpha1.RemediationStateSucceeded,
		v1alpha1.RemediationStateFailed,
		v1alpha1.RemediationStateSimulated,
	} {
		rem := &v1alpha1.Remediation{
			ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "remedik"},
			Status:     v1alpha1.RemediationStatus{State: state},
		}
		if view := buildRemediation(rem, nil, now); view.Approval != nil {
			t.Errorf("%s carries approval commands", state)
		}
	}
}

// The page must not say the same sentence twice: a record awaiting approval had
// its status message as both the summary and the raw message under it.
func TestRemediationView_TheMessageIsNotPrintedTwice(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	rem := &v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "remedik"},
		Status: v1alpha1.RemediationStatus{
			State:   v1alpha1.RemediationStateAwaitingApproval,
			Message: "waiting for approval; 3h46m6s left before this escalates",
		},
	}

	view := buildRemediation(rem, nil, now)

	if !strings.Contains(view.Summary, "waiting for approval") {
		t.Fatalf("summary = %q, want the status message", view.Summary)
	}
	if view.ShowRawMessage() {
		t.Error("the raw message is shown as well as the summary that already is it")
	}
}
