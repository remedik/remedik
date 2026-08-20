package dashboard

import (
	"io"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/remedik/remedik/api/v1alpha1"
)

const (
	testNamespace = "remedik"
	testToken     = "s3cr3t-dashboard-token"
)

// testNow fixes the clock so that every rendered age and duration in these
// tests is a value, not a race.
func testNow() time.Time { return time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC) }

// quietLogger keeps the handler's expected warnings out of the test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func at(minutesAgo int) metav1.Time {
	return metav1.NewTime(testNow().Add(-time.Duration(minutesAgo) * time.Minute))
}

func ptr[T any](v T) *T { return &v }

// simulatedRemediation is a dry-run record: a plan, and nothing touched.
func simulatedRemediation(name, target string, minutesAgo int) v1alpha1.Remediation {
	return v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         testNamespace,
			CreationTimestamp: at(minutesAgo),
			Labels: map[string]string{
				v1alpha1.LabelStrategy:    "pod-crashloop",
				v1alpha1.LabelFingerprint: "fp-" + name,
			},
		},
		Spec: v1alpha1.RemediationSpec{
			StrategyName: "pod-crashloop",
			Target:       target,
			DryRun:       true,
			Steps:        []v1alpha1.Step{{Action: "deployment.restart"}},
			Alert: v1alpha1.AlertRef{
				Fingerprint: "fp-" + name,
				Name:        "KubePodCrashLooping",
				Labels: map[string]string{
					"namespace":  "payments",
					"deployment": "api",
					"severity":   "warning",
				},
				StartsAt: ptr(at(minutesAgo + 5)),
			},
		},
		Status: v1alpha1.RemediationStatus{
			State:       v1alpha1.RemediationStateSimulated,
			Attempt:     1,
			StartedAt:   ptr(at(minutesAgo)),
			CompletedAt: ptr(at(minutesAgo)),
			Steps: []v1alpha1.StepStatus{{
				Index:       0,
				Action:      "deployment.restart",
				Target:      target,
				Phase:       v1alpha1.StepPhaseSimulated,
				Plan:        "patch deployment " + target + " restartedAt annotation",
				Kubectl:     "kubectl rollout restart deployment/api -n payments",
				Outputs:     map[string]string{"replicas": "3"},
				StartedAt:   ptr(at(minutesAgo)),
				CompletedAt: ptr(at(minutesAgo)),
			}},
		},
	}
}

// succeededRemediation is a real run that worked.
func succeededRemediation(name string, minutesAgo int) v1alpha1.Remediation {
	rem := simulatedRemediation(name, "deployment/checkout/web", minutesAgo)
	rem.Spec.StrategyName = "checkout-restart"
	rem.Spec.DryRun = false
	rem.Labels[v1alpha1.LabelStrategy] = "checkout-restart"
	rem.Status.State = v1alpha1.RemediationStateSucceeded
	rem.Status.Steps[0].Phase = v1alpha1.StepPhaseSucceeded
	rem.Status.Steps[0].Plan = "restarted deployment checkout/web"
	rem.Status.Steps[0].Verified = "3/3 replicas updated, available and ready"
	return rem
}

// failedRemediation stopped at its second step, so the third never ran.
func failedRemediation(name string, minutesAgo int) v1alpha1.Remediation {
	return v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         testNamespace,
			CreationTimestamp: at(minutesAgo),
			Labels:            map[string]string{v1alpha1.LabelStrategy: "pod-crashloop"},
		},
		Spec: v1alpha1.RemediationSpec{
			StrategyName: "pod-crashloop",
			Target:       "deployment/payments/api",
			Retries:      1,
			Steps: []v1alpha1.Step{
				{Action: "deployment.restart"},
				{Action: "deployment.restart", With: map[string]string{"deployment": "worker"}},
				{Action: "deployment.restart", With: map[string]string{"deployment": "cache"}},
			},
			Alert: v1alpha1.AlertRef{Fingerprint: "fp-" + name, Name: "KubePodCrashLooping"},
		},
		Status: v1alpha1.RemediationStatus{
			State:       v1alpha1.RemediationStateFailed,
			Reason:      v1alpha1.ReasonStepFailed,
			Message:     `deployments.apps "worker" not found`,
			Attempt:     2,
			StartedAt:   ptr(at(minutesAgo)),
			CompletedAt: ptr(at(minutesAgo - 1)),
			Steps: []v1alpha1.StepStatus{
				{
					Index: 0, Action: "deployment.restart",
					Target:  "deployment/payments/api",
					Phase:   v1alpha1.StepPhaseSucceeded,
					Plan:    "restarted deployment payments/api",
					Kubectl: "kubectl rollout restart deployment/api -n payments",
				},
				{
					Index: 1, Action: "deployment.restart",
					Phase:   v1alpha1.StepPhaseFailed,
					Message: `deployments.apps "worker" not found`,
				},
				{Index: 2, Action: "deployment.restart", Phase: v1alpha1.StepPhaseSkipped},
			},
		},
	}
}

// pendingRemediation has been created but not reconciled yet.
func pendingRemediation(name string, minutesAgo int) v1alpha1.Remediation {
	return v1alpha1.Remediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         testNamespace,
			CreationTimestamp: at(minutesAgo),
		},
		Spec: v1alpha1.RemediationSpec{
			StrategyName: "pod-crashloop",
			Target:       "deployment/payments/api",
			Steps:        []v1alpha1.Step{{Action: "deployment.restart"}},
			Alert:        v1alpha1.AlertRef{Fingerprint: "fp-" + name, Name: "KubePodCrashLooping"},
		},
	}
}

// awaitingRemediation is a record holding for a person. minutesLeft of zero
// gives it a deadline that has already passed; a negative one gives it no
// deadline at all, which the engine treats as expired on sight.
func awaitingRemediation(name string, minutesAgo, minutesLeft int) v1alpha1.Remediation {
	rem := simulatedRemediation(name, "deployment/payments/api", minutesAgo)
	rem.Spec.StrategyName = "payments-restart"
	rem.Spec.DryRun = false
	rem.Labels[v1alpha1.LabelStrategy] = "payments-restart"
	rem.Spec.Steps = []v1alpha1.Step{
		{Action: "deployment.restart", With: map[string]string{"deployment": "api"}},
	}
	// Nothing has been resolved: that happens when it is approved, against the
	// cluster as it is then.
	rem.Status = v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateAwaitingApproval}
	if minutesLeft >= 0 {
		deadline := metav1.NewTime(testNow().Add(time.Duration(minutesLeft) * time.Minute))
		rem.Spec.ApprovalDeadline = &deadline
	}
	return rem
}

// approvalStrategy gates itself on a person.
func approvalStrategy() v1alpha1.RemediationStrategy {
	strategy := enabledStrategy()
	strategy.Name = "payments-restart"
	strategy.Spec.Execution = v1alpha1.Execution{Mode: v1alpha1.ExecutionModeApproval}
	return strategy
}

func enabledStrategy() v1alpha1.RemediationStrategy {
	return v1alpha1.RemediationStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-crashloop",
			CreationTimestamp: at(60 * 24 * 3),
		},
		Spec: v1alpha1.RemediationStrategySpec{
			Enabled:   ptr(true),
			Trigger:   v1alpha1.Trigger{Match: map[string]string{"alertname": "KubePodCrashLooping"}},
			Execution: v1alpha1.Execution{Mode: v1alpha1.ExecutionModeAuto},
			Guards: v1alpha1.Guards{
				Cooldown:   &metav1.Duration{Duration: 15 * time.Minute},
				MaxPerHour: 4,
			},
			Steps:     []v1alpha1.Step{{Action: "deployment.restart"}},
			OnFailure: v1alpha1.OnFailure{Retries: 1},
		},
		Status: v1alpha1.RemediationStrategyStatus{
			LastExecutionTime: ptr(at(30)),
			ExecutionCount:    7,
		},
	}
}

func disabledStrategy() v1alpha1.RemediationStrategy {
	return v1alpha1.RemediationStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "node-drain",
			CreationTimestamp: at(60 * 24),
		},
		Spec: v1alpha1.RemediationStrategySpec{
			Enabled: ptr(false),
			Trigger: v1alpha1.Trigger{Match: map[string]string{"alertname": "NodeNotReady"}},
			Steps:   []v1alpha1.Step{{Action: "deployment.restart"}},
		},
	}
}

// ptrs is what the view pipeline takes. The page builders still accept values
// at their boundary and convert once; everything below them works on pointers,
// because a Remediation is 552 bytes and copying ten thousand of them to
// answer a question about their contents is five and a half megabytes of
// nothing.
func ptrs(remediations []v1alpha1.Remediation) []*v1alpha1.Remediation {
	out := make([]*v1alpha1.Remediation, len(remediations))
	for i := range remediations {
		out[i] = &remediations[i]
	}
	return out
}

// testWindow is the default span the overview describes: a day, one bar per
// hour. Named rather than indexed so a test reads as "the usual window"
// instead of "the first element of a package variable".
func testWindow() Window { return windows[0] }
