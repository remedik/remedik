package workload

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

// scaleClient serves a Deployment, the namespace's autoscalers, and records
// what was patched.
type scaleClient struct {
	client.Client

	deployment  *appsv1.Deployment
	hpa         *autoscalingv2.HorizontalPodAutoscaler
	autoscalers []autoscalingv2.HorizontalPodAutoscaler
	listErr     error

	scalePatches int
	lastScale    string
	hpaPatches   int
	lastHPAPatch string
}

func (c *scaleClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	switch target := obj.(type) {
	case *appsv1.Deployment:
		if c.deployment == nil {
			return apierrors.NewNotFound(schema.GroupResource{Group: "apps"}, key.Name)
		}
		*target = *c.deployment
	case *autoscalingv2.HorizontalPodAutoscaler:
		if c.hpa == nil {
			return apierrors.NewNotFound(schema.GroupResource{Group: "autoscaling"}, key.Name)
		}
		*target = *c.hpa
	default:
		return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
	}
	return nil
}

func (c *scaleClient) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	if c.listErr != nil {
		return c.listErr
	}
	hpas, ok := list.(*autoscalingv2.HorizontalPodAutoscalerList)
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{}, "")
	}
	hpas.Items = append([]autoscalingv2.HorizontalPodAutoscaler(nil), c.autoscalers...)
	return nil
}

func (c *scaleClient) Patch(_ context.Context, obj client.Object, patch client.Patch, _ ...client.PatchOption) error {
	data, _ := patch.Data(obj)
	c.hpaPatches++
	c.lastHPAPatch = string(data)
	return nil
}

func (c *scaleClient) SubResource(name string) client.SubResourceClient {
	return &scaleSubResource{parent: c, name: name}
}

type scaleSubResource struct {
	client.SubResourceClient

	parent *scaleClient
	name   string
}

func (s *scaleSubResource) Patch(
	_ context.Context, obj client.Object, patch client.Patch, _ ...client.SubResourcePatchOption,
) error {
	if s.name != "scale" {
		return apierrors.NewNotFound(schema.GroupResource{}, s.name)
	}
	data, _ := patch.Data(obj)
	s.parent.scalePatches++
	s.parent.lastScale = string(data)
	return nil
}

func scalableDeployment(replicas, available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "api"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status:     appsv1.DeploymentStatus{AvailableReplicas: available},
	}
}

func autoscalerFor(name, deployment string, ceiling int32) autoscalingv2.HorizontalPodAutoscaler {
	return autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: name},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MaxReplicas:    ceiling,
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: "Deployment", Name: deployment},
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: ceiling},
	}
}

func scaleRequest(params action.Params) action.Request {
	return action.Request{Target: target, Params: params}
}

func TestDeploymentScale_SetsAndIncreases(t *testing.T) {
	tests := []struct {
		name    string
		current int32
		params  action.Params
		want    string
	}{
		{
			name:    "absolute",
			current: 2,
			params:  action.Params{ReplicasParam: "5"},
			want:    `{"spec":{"replicas":5}}`,
		},
		{
			name:    "relative",
			current: 2,
			params:  action.Params{IncreaseByParam: "3", MaxParam: "10"},
			want:    `{"spec":{"replicas":5}}`,
		},
		{
			name:    "relative, capped by the ceiling",
			current: 8,
			params:  action.Params{IncreaseByParam: "5", MaxParam: "10"},
			want:    `{"spec":{"replicas":10}}`,
		},
		{
			// Rounded up, so a 10% increase on three replicas adds one
			// rather than nothing.
			name:    "percentage rounds up",
			current: 3,
			params:  action.Params{IncreasePercentParam: "10", MaxParam: "10"},
			want:    `{"spec":{"replicas":4}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &scaleClient{deployment: scalableDeployment(tc.current, tc.current)}
			a := NewDeploymentScale(c)

			if _, err := a.Execute(context.Background(), scaleRequest(tc.params)); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if c.scalePatches != 1 {
				t.Fatalf("patched the scale subresource %d times, want 1", c.scalePatches)
			}
			if c.lastScale != tc.want {
				t.Errorf("patch = %s, want %s", c.lastScale, tc.want)
			}
		})
	}
}

// Setting replicas on an autoscaled Deployment is reverted on the next
// interval, so the remediation records success and nothing sticks.
func TestDeploymentScale_RefusesAnAutoscaledWorkload(t *testing.T) {
	c := &scaleClient{
		deployment:  scalableDeployment(2, 2),
		autoscalers: []autoscalingv2.HorizontalPodAutoscaler{autoscalerFor("api", "api", 10)},
	}
	a := NewDeploymentScale(c)

	for _, call := range []struct {
		name string
		run  func() (action.Result, error)
	}{
		{"Plan", func() (action.Result, error) {
			return a.Plan(context.Background(), scaleRequest(action.Params{ReplicasParam: "5"}))
		}},
		{"Execute", func() (action.Result, error) {
			return a.Execute(context.Background(), scaleRequest(action.Params{ReplicasParam: "5"}))
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			_, err := call.run()
			if err == nil {
				t.Fatal("error = nil; the autoscaler would revert this")
			}
			if !strings.Contains(err.Error(), "hpa.scale") {
				t.Errorf("error = %q, want it to point at the action that works", err)
			}
		})
	}

	if c.scalePatches != 0 {
		t.Errorf("scaled %d times despite the refusal", c.scalePatches)
	}
}

// The autoscaler check is a safety property, so being unable to perform it
// is a refusal rather than a shrug.
func TestDeploymentScale_RefusesWhenItCannotCheckForAnAutoscaler(t *testing.T) {
	c := &scaleClient{
		deployment: scalableDeployment(2, 2),
		listErr: apierrors.NewForbidden(
			schema.GroupResource{Group: "autoscaling", Resource: "horizontalpodautoscalers"},
			"", nil),
	}
	a := NewDeploymentScale(c)

	_, err := a.Execute(context.Background(), scaleRequest(action.Params{ReplicasParam: "5"}))
	if err == nil {
		t.Fatal("error = nil; the check could not be performed")
	}
	if !strings.Contains(err.Error(), "cannot tell whether") {
		t.Errorf("error = %q, want it to say the check failed", err)
	}
	if c.scalePatches != 0 {
		t.Error("scaled despite being unable to check")
	}
}

func TestTargetCount_RefusesWhatCannotBeBounded(t *testing.T) {
	tests := []struct {
		name    string
		params  action.Params
		current int32
		wantErr string
	}{
		{name: "nothing set", params: action.Params{}, wantErr: "must set one of"},
		{
			name:    "two things set",
			params:  action.Params{ReplicasParam: "5", IncreaseByParam: "2"},
			wantErr: "more than one",
		},
		{
			// "Increase by" with no ceiling is an alert storm with a credit
			// card, and a default ceiling would be a number invented for
			// somebody else's budget.
			name:    "relative with no ceiling",
			params:  action.Params{IncreaseByParam: "2"},
			wantErr: "needs a ceiling",
		},
		{
			name:    "already at the ceiling",
			params:  action.Params{IncreaseByParam: "2", MaxParam: "3"},
			current: 3,
			wantErr: "already reached",
		},
		{
			name:    "not a number",
			params:  action.Params{ReplicasParam: "lots"},
			wantErr: "not a whole number",
		},
		{
			name:    "zero",
			params:  action.Params{ReplicasParam: "0"},
			wantErr: "not a positive number",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := targetCount(tc.params, tc.current); err == nil ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// Requested is not running: replicas that cannot schedule are not capacity.
func TestDeploymentScale_VerifyWaitsForAvailability(t *testing.T) {
	c := &scaleClient{deployment: scalableDeployment(5, 5)}
	a := NewDeploymentScale(c)
	a.poll = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	executed := action.Result{Outputs: map[string]string{"replicasAfter": "5"}}
	result, err := a.Verify(ctx, scaleRequest(nil), executed)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !strings.Contains(result.Summary, "5/5 replicas available") {
		t.Errorf("summary = %q", result.Summary)
	}

	// Now the ones that never arrive.
	c.deployment = scalableDeployment(5, 2)
	short, cancelShort := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelShort()

	if _, err := a.Verify(short, scaleRequest(nil), executed); err == nil {
		t.Fatal("Verify() error = nil; replicas that cannot schedule are not capacity")
	}
}

// --------------------------------------------------------------------------
// hpa.scale
// --------------------------------------------------------------------------

func TestHPAScale_RaisesTheCeiling(t *testing.T) {
	c := &scaleClient{hpa: &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "api"},
		Spec:       autoscalingv2.HorizontalPodAutoscalerSpec{MaxReplicas: 10},
		Status:     autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 10},
	}}
	a := NewHPAScale(c)
	hpaTarget := action.Target{Kind: "HorizontalPodAutoscaler", Namespace: "payments", Name: "api"}

	result, err := a.Execute(context.Background(), action.Request{
		Target: hpaTarget,
		Params: action.Params{IncreasePercentParam: "20", MaxParam: "50"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if c.lastHPAPatch != `{"spec":{"maxReplicas":12}}` {
		t.Errorf("patch = %s, want maxReplicas 12", c.lastHPAPatch)
	}
	if result.Outputs["maxReplicasBefore"] != "10" {
		t.Errorf("outputs = %v, want the previous ceiling", result.Outputs)
	}
	// The autoscaler decides whether to use the headroom; saying so keeps
	// the record honest about what was actually changed.
	if !strings.Contains(result.Summary, "the autoscaler decides") {
		t.Errorf("summary = %q, want it to say what was and was not done", result.Summary)
	}
}

func TestHPAScale_NeverLowersTheCeiling(t *testing.T) {
	c := &scaleClient{hpa: &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "api"},
		Spec:       autoscalingv2.HorizontalPodAutoscalerSpec{MaxReplicas: 20},
	}}
	a := NewHPAScale(c)

	_, err := a.Execute(context.Background(), action.Request{
		Target: action.Target{Kind: "HorizontalPodAutoscaler", Namespace: "payments", Name: "api"},
		Params: action.Params{ReplicasParam: "10"},
	})
	if err == nil {
		t.Fatal("error = nil; lowering an autoscaler's ceiling during an incident is not a remediation")
	}
	if c.hpaPatches != 0 {
		t.Error("patched despite the refusal")
	}
}

func TestHPAScale_Resolve(t *testing.T) {
	a := NewHPAScale(&scaleClient{})

	got, err := a.Resolve(map[string]string{
		"namespace": "payments", "horizontalpodautoscaler": "api",
	}, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.String() != "horizontalpodautoscaler/payments/api" {
		t.Errorf("target = %q", got.String())
	}

	if _, err := a.Resolve(map[string]string{"namespace": "payments"}, nil); err == nil {
		t.Error("error = nil for an alert naming no autoscaler")
	}
}
