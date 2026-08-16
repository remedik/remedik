package workload

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

var fixedClock = time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

// stubClient implements just the client.Client methods this action uses.
// Embedding the interface makes any unexpected call panic loudly, which is
// what a test wants: the action must not reach for anything else.
type stubClient struct {
	client.Client

	deployment *appsv1.Deployment
	getErr     error

	patchErr    error
	patchCalls  int
	lastPatched string
}

func (c *stubClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if c.getErr != nil {
		return c.getErr
	}
	d, ok := obj.(*appsv1.Deployment)
	if !ok {
		return errors.New("unexpected object type")
	}
	if c.deployment == nil {
		return notFound(key.Name)
	}
	*d = *c.deployment
	return nil
}

func (c *stubClient) Patch(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) error {
	c.patchCalls++
	c.lastPatched = obj.GetNamespace() + "/" + obj.GetName()
	return c.patchErr
}

func notFound(name string) error {
	return apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, name)
}

func forbidden(name string) error {
	return apierrors.NewForbidden(
		schema.GroupResource{Group: "apps", Resource: "deployments"}, name, errors.New("RBAC denied"))
}

func deployment(namespace, name string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, ResourceVersion: "42"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
}

var target = action.Target{Kind: "Deployment", Namespace: "payments", Name: "api"}

func TestDeploymentRestart_Resolve(t *testing.T) {
	a := NewDeploymentRestart(&stubClient{}, nil)

	tests := []struct {
		name    string
		labels  map[string]string
		params  action.Params
		want    string
		wantErr string
	}{
		{
			name:   "from alert labels",
			labels: map[string]string{"namespace": "payments", "deployment": "api"},
			want:   "deployment/payments/api",
		},
		{
			name:   "step parameters win over labels",
			labels: map[string]string{"namespace": "payments", "deployment": "api"},
			params: action.Params{"namespace": "checkout", "deployment": "web"},
			want:   "deployment/checkout/web",
		},
		{
			name:   "parameters can supply what the alert lacks",
			labels: map[string]string{"namespace": "payments"},
			params: action.Params{"deployment": "api"},
			want:   "deployment/payments/api",
		},
		{
			name:    "no namespace anywhere",
			labels:  map[string]string{"deployment": "api"},
			wantErr: "no namespace",
		},
		{
			// A pod-scoped alert does not name its owner, and guessing the
			// Deployment from a pod name would restart the wrong workload.
			name:    "a pod label is not enough",
			labels:  map[string]string{"namespace": "payments", "pod": "api-7d9f-xyz"},
			wantErr: "no deployment",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.Resolve(tc.labels, tc.params)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Resolve() error = nil, want %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v, want nil", err)
			}
			if got.String() != tc.want {
				t.Errorf("Resolve() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeploymentRestart_PlanDescribesWithoutMutating(t *testing.T) {
	c := &stubClient{deployment: deployment("payments", "api", 3)}
	a := NewDeploymentRestart(c, func() time.Time { return fixedClock })

	plan, err := a.Plan(context.Background(), action.Request{Target: target, Params: nil})
	if err != nil {
		t.Fatalf("Plan() error = %v, want nil", err)
	}
	if c.patchCalls != 0 {
		t.Errorf("Plan patched the cluster %d times", c.patchCalls)
	}
	for _, want := range []string{"deployment/payments/api", "3 replicas", RestartAnnotation} {
		if !strings.Contains(plan.Summary, want) {
			t.Errorf("plan = %q, want it to mention %q", plan.Summary, want)
		}
	}
}

func TestDeploymentRestart_Execute(t *testing.T) {
	c := &stubClient{deployment: deployment("payments", "api", 2)}
	a := NewDeploymentRestart(c, func() time.Time { return fixedClock })

	got, err := a.Execute(context.Background(), action.Request{Target: target, Params: nil})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if c.patchCalls != 1 {
		t.Fatalf("patched %d times, want 1", c.patchCalls)
	}
	if c.lastPatched != "payments/api" {
		t.Errorf("patched %q, want payments/api", c.lastPatched)
	}
	for _, want := range []string{"restarted deployment/payments/api", "2026-08-15T12:00:00Z", "resourceVersion 42"} {
		if !strings.Contains(got.Summary, want) {
			t.Errorf("result = %q, want it to mention %q", got.Summary, want)
		}
	}
}

func TestDeploymentRestart_MissingTarget(t *testing.T) {
	c := &stubClient{} // no deployment stored -> Get returns NotFound
	a := NewDeploymentRestart(c, nil)

	for _, tc := range []struct {
		name string
		call func() (action.Result, error)
	}{
		{"Plan", func() (action.Result, error) {
			return a.Plan(context.Background(), action.Request{Target: target, Params: nil})
		}},
		{"Execute", func() (action.Result, error) {
			return a.Execute(context.Background(), action.Request{Target: target, Params: nil})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.call()
			if err == nil {
				t.Fatal("error = nil, want a not-found error")
			}
			if !strings.Contains(err.Error(), "does not exist") {
				t.Errorf("error = %q, want it to say the target does not exist", err)
			}
			if c.patchCalls != 0 {
				t.Errorf("patched %d times despite a missing target", c.patchCalls)
			}
		})
	}
}

// An RBAC denial must be reported clearly and must not crash the operator:
// the engine records it on the Remediation and carries on.
func TestDeploymentRestart_PermissionDenied(t *testing.T) {
	t.Run("on read", func(t *testing.T) {
		a := NewDeploymentRestart(&stubClient{getErr: forbidden("api")}, nil)
		_, err := a.Execute(context.Background(), action.Request{Target: target, Params: nil})
		if err == nil || !strings.Contains(err.Error(), "not permitted to read") {
			t.Errorf("error = %v, want a clear permission error", err)
		}
	})

	t.Run("on patch", func(t *testing.T) {
		c := &stubClient{deployment: deployment("payments", "api", 1), patchErr: forbidden("api")}
		a := NewDeploymentRestart(c, nil)
		_, err := a.Execute(context.Background(), action.Request{Target: target, Params: nil})
		if err == nil || !strings.Contains(err.Error(), "not permitted to patch") {
			t.Errorf("error = %v, want a clear permission error", err)
		}
	})
}

func TestDeploymentRestart_DefaultsReplicasWhenUnset(t *testing.T) {
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "api"}}
	a := NewDeploymentRestart(&stubClient{deployment: d}, nil)

	plan, err := a.Plan(context.Background(), action.Request{Target: target, Params: nil})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !strings.Contains(plan.Summary, "1 replicas") {
		t.Errorf("plan = %q, want an unset replica count to read as 1", plan.Summary)
	}
}

func TestDeploymentRestart_Name(t *testing.T) {
	if got := NewDeploymentRestart(&stubClient{}, nil).Name(); got != "deployment.restart" {
		t.Errorf("Name() = %q, want deployment.restart", got)
	}
}
