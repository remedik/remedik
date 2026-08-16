package workload

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

// rollingClient hands out a scripted sequence of Deployment states, one per
// Get, so a test can watch a rollout progress without a cluster.
type rollingClient struct {
	client.Client

	states []*appsv1.Deployment
	gets   int
}

func (c *rollingClient) Get(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	d, ok := obj.(*appsv1.Deployment)
	if !ok {
		return notFound("unexpected type")
	}
	index := min(c.gets, len(c.states)-1)
	c.gets++
	*d = *c.states[index]
	return nil
}

// rollout builds one moment in a Deployment's life.
func rollout(generation, observed int64, want, updated, replicas, available, ready int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "api", Generation: generation},
		Spec:       appsv1.DeploymentSpec{Replicas: &want},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: observed,
			UpdatedReplicas:    updated,
			Replicas:           replicas,
			AvailableReplicas:  available,
			ReadyReplicas:      ready,
		},
	}
}

func verifier(states ...*appsv1.Deployment) *DeploymentRestart {
	a := NewDeploymentRestart(&rollingClient{states: states}, nil)
	// Tests must not wait on wall-clock; the polling interval is the only
	// thing that would make them slow.
	a.poll = time.Millisecond
	return a
}

func TestRolloutState(t *testing.T) {
	tests := []struct {
		name       string
		deployment *appsv1.Deployment
		wantDone   bool
		wantSays   string
	}{
		{
			// The generation check comes first and matters most: a status
			// left over from before the patch describes the old rollout,
			// and would otherwise read as a finished new one.
			name:       "the controller has not seen the change yet",
			deployment: rollout(4, 3, 3, 3, 3, 3, 3),
			wantSays:   "observe generation 4",
		},
		{
			name:       "new pods are still being created",
			deployment: rollout(4, 4, 3, 1, 3, 3, 3),
			wantSays:   "1/3 replicas updated",
		},
		{
			name:       "old pods are still going away",
			deployment: rollout(4, 4, 3, 3, 5, 3, 3),
			wantSays:   "2 old replicas still terminating",
		},
		{
			name:       "new pods are not available yet",
			deployment: rollout(4, 4, 3, 3, 3, 1, 1),
			wantSays:   "1/3 replicas available",
		},
		{
			name:       "finished",
			deployment: rollout(4, 4, 3, 3, 3, 3, 3),
			wantDone:   true,
			wantSays:   "3/3 replicas updated, available and ready",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			says, done := rolloutState(tc.deployment)
			if done != tc.wantDone {
				t.Errorf("done = %v, want %v (%q)", done, tc.wantDone, says)
			}
			if !strings.Contains(says, tc.wantSays) {
				t.Errorf("state = %q, want it to mention %q", says, tc.wantSays)
			}
		})
	}
}

func TestDeploymentRestart_VerifyWaitsForTheRollout(t *testing.T) {
	a := verifier(
		rollout(4, 3, 3, 3, 3, 3, 3), // not observed yet
		rollout(4, 4, 3, 1, 3, 1, 1), // half way
		rollout(4, 4, 3, 3, 3, 3, 3), // done
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := a.Verify(ctx, target, nil)
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}
	if !strings.Contains(result.Summary, "3/3 replicas updated, available and ready") {
		t.Errorf("summary = %q, want the completed rollout", result.Summary)
	}
	if result.Outputs["readyReplicas"] != "3" {
		t.Errorf("outputs = %v, want readyReplicas 3", result.Outputs)
	}
}

func TestDeploymentRestart_VerifyFailsWhenTheRolloutStalls(t *testing.T) {
	// A rollout that never progresses: the pods never become available.
	a := verifier(rollout(4, 4, 3, 3, 3, 1, 1))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	result, err := a.Verify(ctx, target, nil)
	if err == nil {
		t.Fatal("Verify() error = nil; a rollout that never completes is not a success")
	}
	// Saying how far it got is more useful than saying that time ran out.
	if !strings.Contains(err.Error(), "1/3 replicas available") {
		t.Errorf("error = %q, want it to say how far the rollout got", err)
	}
	if !strings.Contains(result.Summary, "1/3 replicas available") {
		t.Errorf("summary = %q, want the last observed state", result.Summary)
	}
}

func TestDeploymentRestart_ReportsTheEquivalentCommand(t *testing.T) {
	c := &stubClient{deployment: deployment("payments", "api", 3)}
	a := NewDeploymentRestart(c, func() time.Time { return fixedClock })

	want := "kubectl rollout restart deployment/api -n payments"

	planned, err := a.Plan(context.Background(), target, nil)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if planned.Kubectl != want {
		t.Errorf("Plan kubectl = %q, want %q", planned.Kubectl, want)
	}

	executed, err := a.Execute(context.Background(), target, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if executed.Kubectl != want {
		t.Errorf("Execute kubectl = %q, want %q", executed.Kubectl, want)
	}
	if executed.Outputs["restartedAt"] != fixedClock.Format(time.RFC3339) {
		t.Errorf("outputs = %v, want the restart timestamp", executed.Outputs)
	}
	if executed.Outputs["replicas"] != "3" {
		t.Errorf("outputs = %v, want replicas 3", executed.Outputs)
	}
}

func TestVerifyTimeout(t *testing.T) {
	tests := []struct {
		name    string
		params  action.Params
		want    time.Duration
		wantErr string
	}{
		{name: "unset", want: action.DefaultVerifyTimeout},
		{name: "set", params: action.Params{action.VerifyTimeoutParam: "90s"}, want: 90 * time.Second},
		{
			name:    "no unit",
			params:  action.Params{action.VerifyTimeoutParam: "30"},
			wantErr: "verifyTimeout",
		},
		{
			name:    "negative",
			params:  action.Params{action.VerifyTimeoutParam: "-1m"},
			wantErr: "not a positive duration",
		},
		{
			// Executions are serialised, so a long check is time no other
			// remediation can use.
			name:    "beyond the cap",
			params:  action.Params{action.VerifyTimeoutParam: "30m"},
			wantErr: "exceeds the maximum",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := action.VerifyTimeout(tc.params)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("timeout = %s, want %s", got, tc.want)
			}
		})
	}
}
