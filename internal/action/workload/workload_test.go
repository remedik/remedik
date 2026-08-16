package workload

import (
	"context"
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

// kindClient serves whichever workload kind is asked for, and records what
// was patched.
type kindClient struct {
	client.Client

	deployment  *appsv1.Deployment
	statefulSet *appsv1.StatefulSet
	daemonSet   *appsv1.DaemonSet

	patches     int
	lastPatched string
}

func (c *kindClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	missing := apierrors.NewNotFound(schema.GroupResource{Group: "apps"}, key.Name)

	switch target := obj.(type) {
	case *appsv1.Deployment:
		if c.deployment == nil {
			return missing
		}
		*target = *c.deployment
	case *appsv1.StatefulSet:
		if c.statefulSet == nil {
			return missing
		}
		*target = *c.statefulSet
	case *appsv1.DaemonSet:
		if c.daemonSet == nil {
			return missing
		}
		*target = *c.daemonSet
	default:
		return missing
	}
	return nil
}

func (c *kindClient) Patch(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) error {
	c.patches++
	c.lastPatched = obj.GetObjectKind().GroupVersionKind().Kind + " " + obj.GetName()
	if c.lastPatched == " "+obj.GetName() {
		// The typed objects the fetch produced carry no TypeMeta; name the
		// kind from the Go type instead so the assertion stays readable.
		switch obj.(type) {
		case *appsv1.Deployment:
			c.lastPatched = "Deployment " + obj.GetName()
		case *appsv1.StatefulSet:
			c.lastPatched = "StatefulSet " + obj.GetName()
		case *appsv1.DaemonSet:
			c.lastPatched = "DaemonSet " + obj.GetName()
		}
	}
	return nil
}

// rolledOut is a Deployment whose rollout has already finished — the
// terminal state a verification test needs to reach.
func rolledOut(namespace, name string, replicas int32) *appsv1.Deployment {
	d := deployment(namespace, name, replicas)
	d.Status = appsv1.DeploymentStatus{
		Replicas: replicas, UpdatedReplicas: replicas,
		AvailableReplicas: replicas, ReadyReplicas: replicas,
	}
	return d
}

func statefulSet(name string, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "db", Name: name, ResourceVersion: "7"},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
		Status: appsv1.StatefulSetStatus{
			Replicas: replicas, UpdatedReplicas: replicas,
			AvailableReplicas: replicas, ReadyReplicas: replicas,
		},
	}
}

func daemonSet(name string, nodes int32) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: name, ResourceVersion: "9"},
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: nodes, UpdatedNumberScheduled: nodes,
			CurrentNumberScheduled: nodes, NumberAvailable: nodes, NumberReady: nodes,
		},
	}
}

func TestWorkloadRestart_ResolvesTheKindFromTheAlert(t *testing.T) {
	a := NewWorkloadRestart(&kindClient{}, nil)

	tests := []struct {
		name    string
		labels  map[string]string
		params  action.Params
		want    string
		wantErr string
	}{
		{
			// The label naming the object also names its kind: that is how
			// the kubernetes-mixin alerts are built.
			name:   "a statefulset label",
			labels: map[string]string{"namespace": "db", "statefulset": "postgres"},
			want:   "statefulset/db/postgres",
		},
		{
			name:   "a daemonset label",
			labels: map[string]string{"namespace": "kube-system", "daemonset": "node-exporter"},
			want:   "daemonset/kube-system/node-exporter",
		},
		{
			name:   "a deployment label",
			labels: map[string]string{"namespace": "payments", "deployment": "api"},
			want:   "deployment/payments/api",
		},
		{
			name:   "the step names the kind explicitly",
			labels: map[string]string{"namespace": "db"},
			params: action.Params{KindParam: "sts", NameParam: "postgres"},
			want:   "statefulset/db/postgres",
		},
		{
			name:    "a kind this action does not handle",
			labels:  map[string]string{"namespace": "db"},
			params:  action.Params{KindParam: "cronjob", NameParam: "nightly"},
			wantErr: "unsupported kind",
		},
		{
			// An alert naming only a pod is exactly where guessing goes
			// wrong, so it is refused rather than guessed.
			name:    "only a pod",
			labels:  map[string]string{"namespace": "payments", "pod": "api-7d9f8-x2k1"},
			wantErr: "no workload",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.Resolve(tc.labels, tc.params)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if got.String() != tc.want {
				t.Errorf("target = %q, want %q", got.String(), tc.want)
			}
		})
	}
}

// deployment.restart keeps its meaning: it acts on Deployments whatever the
// alert carries, so its RBAC stays to Deployments alone.
func TestDeploymentRestart_IgnoresOtherWorkloadLabels(t *testing.T) {
	a := NewDeploymentRestart(&kindClient{}, nil)

	got, err := a.Resolve(map[string]string{
		"namespace": "db", "statefulset": "postgres", "deployment": "api",
	}, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.String() != "deployment/db/api" {
		t.Errorf("target = %q, want the Deployment", got.String())
	}

	if _, err := a.Resolve(map[string]string{"namespace": "db", "statefulset": "postgres"}, nil); err == nil {
		t.Error("error = nil; deployment.restart must not act on a StatefulSet")
	}
}

func TestWorkloadRestart_PatchesEveryKind(t *testing.T) {
	tests := []struct {
		name      string
		client    *kindClient
		target    action.Target
		wantPatch string
		wantPlan  string
		wantCmd   string
	}{
		{
			name:      "deployment",
			client:    &kindClient{deployment: rolledOut("payments", "api", 3)},
			target:    action.Target{Kind: "Deployment", Namespace: "payments", Name: "api"},
			wantPatch: "Deployment api",
			wantPlan:  "3 replicas",
			wantCmd:   "kubectl rollout restart deployment/api -n payments",
		},
		{
			name:      "statefulset",
			client:    &kindClient{statefulSet: statefulSet("postgres", 2)},
			target:    action.Target{Kind: "StatefulSet", Namespace: "db", Name: "postgres"},
			wantPatch: "StatefulSet postgres",
			wantPlan:  "2 replicas",
			wantCmd:   "kubectl rollout restart statefulset/postgres -n db",
		},
		{
			name:      "daemonset",
			client:    &kindClient{daemonSet: daemonSet("node-exporter", 5)},
			target:    action.Target{Kind: "DaemonSet", Namespace: "kube-system", Name: "node-exporter"},
			wantPatch: "DaemonSet node-exporter",
			// A DaemonSet has one pod per node, so it is described in nodes:
			// telling an operator "5 replicas" would be answering in
			// somebody else's vocabulary.
			wantPlan: "5 nodes",
			wantCmd:  "kubectl rollout restart daemonset/node-exporter -n kube-system",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewWorkloadRestart(tc.client, func() time.Time { return fixedClock })

			planned, err := a.Plan(context.Background(), tc.target, nil)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			if !strings.Contains(planned.Summary, tc.wantPlan) {
				t.Errorf("plan = %q, want it to mention %q", planned.Summary, tc.wantPlan)
			}
			if planned.Kubectl != tc.wantCmd {
				t.Errorf("kubectl = %q, want %q", planned.Kubectl, tc.wantCmd)
			}
			if tc.client.patches != 0 {
				t.Errorf("Plan patched the cluster %d times", tc.client.patches)
			}

			if _, err := a.Execute(context.Background(), tc.target, nil); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if tc.client.patches != 1 {
				t.Fatalf("patched %d times, want 1", tc.client.patches)
			}
			if tc.client.lastPatched != tc.wantPatch {
				t.Errorf("patched %q, want %q", tc.client.lastPatched, tc.wantPatch)
			}

			// Every kind reports the same completed-rollout sentence, so a
			// reader does not have to learn three vocabularies.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			verified, err := a.Verify(ctx, tc.target, nil, action.Result{})
			if err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if !strings.Contains(verified.Summary, "updated, available and ready") {
				t.Errorf("verified = %q", verified.Summary)
			}
		})
	}
}

func TestWorkloadRestart_Name(t *testing.T) {
	if got := NewWorkloadRestart(&kindClient{}, nil).Name(); got != "workload.restart" {
		t.Errorf("Name() = %q, want workload.restart", got)
	}
	if got := NewDeploymentRestart(&kindClient{}, nil).Name(); got != "deployment.restart" {
		t.Errorf("Name() = %q, want deployment.restart", got)
	}
}
