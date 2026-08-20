// The dashboard, with a cluster's worth of made-up history, on localhost, with
// no cluster at all.
//
// It exists because the two checks that only a browser can make -- the console,
// which is the only place a Content-Security-Policy violation is reported, and
// the layout, which no handler test lays out -- needed a kind cluster, a Helm
// install and a port-forward first. That is fifteen minutes before the first
// look at a page, which is how a stylesheet rule that never matched survived
// for months.
//
// This is one `go run` and it serves every page: repeats to collapse, failures
// with the messages the explainer has rules for, records waiting for a person
// with a deadline that is actually counting down, and enough namespaces to push
// the filter past the threshold where it becomes a select.
//
// It is not a fixture for tests -- those have their own, closer to what they
// assert. It is the thing you point Chrome at.
//
// Usage:
//
//	go run ./hack/dev-dashboard        # then http://localhost:8099
//	node hack/browser-check.mjs        # with BASE=http://localhost:8099
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/dashboard"
)

type reader struct {
	remediations []v1alpha1.Remediation
	strategies   []v1alpha1.RemediationStrategy
}

func (r *reader) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	target, ok := obj.(*v1alpha1.Remediation)
	if !ok {
		return fmt.Errorf("unsupported kind %T", obj)
	}
	for i := range r.remediations {
		if r.remediations[i].Name == key.Name {
			r.remediations[i].DeepCopyInto(target)
			return nil
		}
	}
	return apierrors.NewNotFound(
		schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "remediations"}, key.Name)
}

func (r *reader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	switch typed := list.(type) {
	case *v1alpha1.RemediationList:
		typed.Items = r.remediations
	case *v1alpha1.RemediationStrategyList:
		typed.Items = r.strategies
	}
	return nil
}

func main() {
	now := time.Now()
	data := seed(now)

	ui, err := dashboard.New(dashboard.Config{
		Reader:    data,
		Namespace: "remedik",
		Cluster:   "dev-kind",
		Version:   "browser-check",
		Posture:   dashboard.Posture{DryRun: true, Live: []string{"payments", "checkout"}},
		Links: []dashboard.Link{
			{Name: "Grafana", URL: "https://grafana.example.com/d/k8s?var-namespace={namespace}&from={from}&to={to}"},
			{Name: "Refused", URL: "javascript:alert(1)"},
		},
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	if err != nil {
		panic(err)
	}

	addr := ":8099"
	fmt.Println("dashboard on http://localhost" + addr)
	if err := http.ListenAndServe(addr, ui.Mux()); err != nil {
		panic(err)
	}
}

func at(d time.Duration, now time.Time) *metav1.Time {
	t := metav1.NewTime(now.Add(-d))
	return &t
}

func seed(now time.Time) *reader {
	data := &reader{}

	namespaces := []string{
		"payments", "checkout", "search", "billing", "ingest",
		"etl", "identity", "pricing", "ml-serving", "scheduler", "workers", "notify",
	}
	strategies := []string{"pod-crashloop", "deployment-oom", "job-failed", "statefulset-stuck"}

	for i := range 140 {
		ns := namespaces[i%len(namespaces)]
		strategy := strategies[i%len(strategies)]
		age := time.Duration(i*17) * time.Minute
		rem := v1alpha1.Remediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              fmt.Sprintf("%s-%04d", strategy, i),
				Namespace:         "remedik",
				CreationTimestamp: *at(age, now),
			},
			Spec: v1alpha1.RemediationSpec{
				StrategyName: strategy,
				Target:       fmt.Sprintf("deployment/%s/api", ns),
				Steps:        []v1alpha1.Step{{Action: "deployment.restart", With: map[string]string{"deployment": "api"}}},
				Alert: v1alpha1.AlertRef{
					Name:        "KubePodCrashLooping",
					Fingerprint: fmt.Sprintf("fp-%04d", i),
					StartsAt:    at(age+5*time.Minute, now),
					Labels:      map[string]string{"namespace": ns, "severity": "warning", "deployment": "api"},
				},
			},
			Status: v1alpha1.RemediationStatus{
				Attempt:     1,
				StartedAt:   at(age, now),
				CompletedAt: at(age-40*time.Second, now),
				Steps: []v1alpha1.StepStatus{{
					Index: 0, Action: "deployment.restart", Target: fmt.Sprintf("deployment/%s/api", ns),
					Phase:   v1alpha1.StepPhaseSucceeded,
					Plan:    "restart deployment/" + ns + "/api by patching its pod template",
					Kubectl: "kubectl rollout restart deployment/api -n " + ns,
					Outputs: map[string]string{"replicas": "3"}, Verified: "3/3 replicas updated, available and ready",
					StartedAt: at(age, now), CompletedAt: at(age-40*time.Second, now),
				}},
			},
		}

		switch {
		case i%7 == 0:
			rem.Status.State = v1alpha1.RemediationStateFailed
			rem.Status.Reason = v1alpha1.ReasonStepFailed
			rem.Status.Message = `deployments.apps "api" not found`
			rem.Status.Steps[0].Phase = v1alpha1.StepPhaseFailed
			rem.Status.Steps[0].Message = rem.Status.Message
			if i%14 == 0 {
				rem.Status.Escalation = &v1alpha1.EscalationStatus{
					Phase: v1alpha1.StepPhaseFailed, Message: "dial tcp: no such host",
					CompletedAt: at(age-30*time.Second, now),
					Steps:       []v1alpha1.StepStatus{{Index: 0, Action: "webhook.call", Phase: v1alpha1.StepPhaseFailed, Message: "no such host"}},
				}
			}
		case i%5 == 0:
			rem.Spec.DryRun = true
			rem.Status.State = v1alpha1.RemediationStateSimulated
			rem.Status.Steps[0].Phase = v1alpha1.StepPhaseSimulated
		default:
			rem.Status.State = v1alpha1.RemediationStateSucceeded
		}
		data.remediations = append(data.remediations, rem)
	}

	// A crash-loop nobody has fixed: the same fact, nine times.
	for i := range 9 {
		age := time.Duration(i+1) * time.Minute
		data.remediations = append(data.remediations, v1alpha1.Remediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              fmt.Sprintf("pod-crashloop-r%02d", i),
				Namespace:         "remedik",
				CreationTimestamp: *at(age, now),
			},
			Spec: v1alpha1.RemediationSpec{
				StrategyName: "pod-crashloop",
				Target:       "deployment/checkout/web",
				Steps:        []v1alpha1.Step{{Action: "deployment.restart"}},
				Alert: v1alpha1.AlertRef{
					Name: "KubePodCrashLooping", Fingerprint: fmt.Sprintf("fp-r%02d", i),
					StartsAt: at(age+3*time.Minute, now),
					Labels:   map[string]string{"namespace": "checkout", "deployment": "web"},
				},
			},
			Status: v1alpha1.RemediationStatus{
				State: v1alpha1.RemediationStateFailed, Reason: v1alpha1.ReasonStepFailed,
				Message: `deployments.apps "web" not found`, Attempt: 2,
				StartedAt: at(age, now), CompletedAt: at(age-20*time.Second, now),
				Steps: []v1alpha1.StepStatus{{
					Index: 0, Action: "deployment.restart", Phase: v1alpha1.StepPhaseFailed,
					Message:   `deployments.apps "web" not found`,
					StartedAt: at(age, now), CompletedAt: at(age-20*time.Second, now),
				}},
			},
		})
	}

	// Three waiting for a person, one of them nearly out of time.
	for i, left := range []time.Duration{40 * time.Second, 4 * time.Minute, 12 * time.Minute} {
		deadline := metav1.NewTime(now.Add(left))
		data.remediations = append(data.remediations, v1alpha1.Remediation{
			ObjectMeta: metav1.ObjectMeta{
				Name:              fmt.Sprintf("payments-restart-%d", i),
				Namespace:         "remedik",
				CreationTimestamp: *at(time.Duration(i+12)*time.Minute, now),
			},
			Spec: v1alpha1.RemediationSpec{
				StrategyName:     "payments-restart",
				Target:           "deployment/payments/checkout-api",
				ApprovalDeadline: &deadline,
				Steps: []v1alpha1.Step{
					{Action: "deployment.restart", With: map[string]string{"deployment": "checkout-api"}},
					{Action: "scale", With: map[string]string{"replicas": "5"}},
				},
				Alert: v1alpha1.AlertRef{Name: "PaymentsLatencyHigh", Fingerprint: "fp-approval", StartsAt: at(9*time.Minute, now)},
			},
			Status: v1alpha1.RemediationStatus{State: v1alpha1.RemediationStateAwaitingApproval},
		})
	}

	enabled := true
	for _, name := range append(strategies, "payments-restart") {
		strategy := v1alpha1.RemediationStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: *at(72*time.Hour, now)},
			Spec: v1alpha1.RemediationStrategySpec{
				Enabled:   &enabled,
				Trigger:   v1alpha1.Trigger{Match: map[string]string{"alertname": "KubePodCrashLooping"}},
				Execution: v1alpha1.Execution{Mode: v1alpha1.ExecutionModeAuto},
				Guards:    v1alpha1.Guards{Cooldown: &metav1.Duration{Duration: 15 * time.Minute}, MaxPerHour: 4},
				Steps:     []v1alpha1.Step{{Action: "deployment.restart"}},
			},
		}
		if name == "payments-restart" {
			strategy.Spec.Execution.Mode = v1alpha1.ExecutionModeApproval
		}
		data.strategies = append(data.strategies, strategy)
	}
	return data
}
