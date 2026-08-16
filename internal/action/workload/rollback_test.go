package workload

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

const deploymentUID = types.UID("dep-1")

// rollbackClient serves a Deployment and its revision history.
type rollbackClient struct {
	client.Client

	deployment  *appsv1.Deployment
	replicaSets []appsv1.ReplicaSet

	patches   int
	lastPatch string
}

func (c *rollbackClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	d, ok := obj.(*appsv1.Deployment)
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
	}
	if c.deployment == nil {
		return apierrors.NewNotFound(schema.GroupResource{Group: "apps"}, key.Name)
	}
	*d = *c.deployment
	return nil
}

func (c *rollbackClient) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	sets, ok := list.(*appsv1.ReplicaSetList)
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{}, "")
	}
	sets.Items = append([]appsv1.ReplicaSet(nil), c.replicaSets...)
	return nil
}

func (c *rollbackClient) Patch(_ context.Context, obj client.Object, patch client.Patch, _ ...client.PatchOption) error {
	data, _ := patch.Data(obj)
	c.patches++
	c.lastPatch = string(data)
	return nil
}

func deployedAt(revision string, mutate ...func(*appsv1.Deployment)) *appsv1.Deployment {
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "payments", Name: "api", UID: deploymentUID,
			Annotations: map[string]string{RevisionAnnotation: revision},
		},
		Spec: appsv1.DeploymentSpec{Replicas: replicasPtr(3)},
	}
	for _, m := range mutate {
		m(d)
	}
	return d
}

func revisionSet(name, revision, image string) appsv1.ReplicaSet {
	controller := true
	return appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "payments", Name: name,
			Annotations: map[string]string{RevisionAnnotation: revision},
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "Deployment", Name: "api", UID: deploymentUID, Controller: &controller,
			}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "api", "pod-template-hash": "abc123"},
				},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: image}}},
			},
		},
	}
}

func replicasPtr(n int32) *int32 { return &n }

func TestDeploymentRollback_PutsThePreviousRevisionBack(t *testing.T) {
	c := &rollbackClient{
		deployment: deployedAt("3"),
		replicaSets: []appsv1.ReplicaSet{
			revisionSet("api-old", "2", "example/api:1.4"),
			revisionSet("api-new", "3", "example/api:1.5"),
		},
	}
	a := NewDeploymentRollback(c)

	result, err := a.Execute(context.Background(), action.Request{Target: target})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if c.patches != 1 {
		t.Fatalf("patched %d times, want 1", c.patches)
	}
	// The old revision's image is what goes back.
	if !strings.Contains(c.lastPatch, "example/api:1.4") {
		t.Errorf("patch = %s, want the previous revision's template", c.lastPatch)
	}
	// pod-template-hash belongs to the ReplicaSet, not the Deployment:
	// leaving it in would make the controller compute a wrong hash.
	if strings.Contains(c.lastPatch, "pod-template-hash") {
		t.Errorf("patch = %s, want the pod-template-hash stripped", c.lastPatch)
	}
	if result.Outputs["rolledBackTo"] != "2" || result.Outputs["fromRevision"] != "3" {
		t.Errorf("outputs = %v, want the revisions recorded", result.Outputs)
	}
	if !strings.Contains(result.Kubectl, "--to-revision=2") {
		t.Errorf("kubectl = %q", result.Kubectl)
	}
}

func TestDeploymentRollback_ToANamedRevision(t *testing.T) {
	c := &rollbackClient{
		deployment: deployedAt("4"),
		replicaSets: []appsv1.ReplicaSet{
			revisionSet("api-1", "1", "example/api:1.1"),
			revisionSet("api-2", "2", "example/api:1.2"),
			revisionSet("api-4", "4", "example/api:1.4"),
		},
	}
	a := NewDeploymentRollback(c)

	if _, err := a.Execute(context.Background(), action.Request{
		Target: target, Params: action.Params{ToRevisionParam: "1"},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(c.lastPatch, "example/api:1.1") {
		t.Errorf("patch = %s, want revision 1", c.lastPatch)
	}
}

// A rollback Argo CD or Flux reverts within minutes is worse than no
// rollback: remedik records success and the outage continues.
func TestDeploymentRollback_RefusesGitOpsManagedWorkloads(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*appsv1.Deployment)
		controller string
	}{
		{
			name:       "Argo CD annotation",
			controller: "Argo CD",
			mutate: func(d *appsv1.Deployment) {
				d.Annotations["argocd.argoproj.io/instance"] = "payments"
			},
		},
		{
			name:       "Flux label",
			controller: "Flux",
			mutate: func(d *appsv1.Deployment) {
				d.Labels = map[string]string{"kustomize.toolkit.fluxcd.io/name": "apps"}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &rollbackClient{
				deployment:  deployedAt("3", tc.mutate),
				replicaSets: []appsv1.ReplicaSet{revisionSet("api-old", "2", "example/api:1.4")},
			}
			a := NewDeploymentRollback(c)

			_, err := a.Execute(context.Background(), action.Request{Target: target})
			if err == nil {
				t.Fatal("error = nil; the GitOps controller would revert this")
			}
			if !strings.Contains(err.Error(), tc.controller) {
				t.Errorf("error = %q, want it to name %s", err, tc.controller)
			}
			if !strings.Contains(err.Error(), "Revert the commit instead") {
				t.Errorf("error = %q, want it to say what to do instead", err)
			}
			if c.patches != 0 {
				t.Error("rolled back despite the refusal")
			}
		})
	}
}

func TestDeploymentRollback_TheRefusalCanBeOverridden(t *testing.T) {
	c := &rollbackClient{
		deployment: deployedAt("3", func(d *appsv1.Deployment) {
			d.Annotations["argocd.argoproj.io/instance"] = "payments"
		}),
		replicaSets: []appsv1.ReplicaSet{revisionSet("api-old", "2", "example/api:1.4")},
	}
	a := NewDeploymentRollback(c)

	if _, err := a.Execute(context.Background(), action.Request{
		Target: target, Params: action.Params{IgnoreGitOpsParam: "true"},
	}); err != nil {
		t.Fatalf("Execute() error = %v, want nil when the step accepts the conflict", err)
	}
	if c.patches != 1 {
		t.Errorf("patched %d times, want 1", c.patches)
	}
}

func TestDeploymentRollback_RefusesWhatItCannotDo(t *testing.T) {
	tests := []struct {
		name        string
		deployment  *appsv1.Deployment
		replicaSets []appsv1.ReplicaSet
		params      action.Params
		wantErr     string
	}{
		{
			name:       "no history kept",
			deployment: deployedAt("3"),
			wantErr:    "no revision history",
		},
		{
			name:        "nothing earlier than the current revision",
			deployment:  deployedAt("1"),
			replicaSets: []appsv1.ReplicaSet{revisionSet("api-1", "1", "example/api:1.1")},
			wantErr:     "nothing to roll back to",
		},
		{
			name:        "a revision that was not kept",
			deployment:  deployedAt("3"),
			replicaSets: []appsv1.ReplicaSet{revisionSet("api-2", "2", "example/api:1.2")},
			params:      action.Params{ToRevisionParam: "1"},
			wantErr:     "has no revision 1 kept",
		},
		{
			name:        "already there",
			deployment:  deployedAt("2"),
			replicaSets: []appsv1.ReplicaSet{revisionSet("api-2", "2", "example/api:1.2")},
			params:      action.Params{ToRevisionParam: "2"},
			wantErr:     "already at revision 2",
		},
		{
			name:        "a revision that is not a number",
			deployment:  deployedAt("3"),
			replicaSets: []appsv1.ReplicaSet{revisionSet("api-2", "2", "example/api:1.2")},
			params:      action.Params{ToRevisionParam: "previous"},
			wantErr:     "not a revision number",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &rollbackClient{deployment: tc.deployment, replicaSets: tc.replicaSets}
			a := NewDeploymentRollback(c)

			// Plan and Execute refuse identically, so a dry run cannot
			// promise a rollback Execute would not perform.
			if _, err := a.Plan(context.Background(),
				action.Request{Target: target, Params: tc.params}); err == nil ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Plan error = %v, want it to mention %q", err, tc.wantErr)
			}
			if _, err := a.Execute(context.Background(),
				action.Request{Target: target, Params: tc.params}); err == nil ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Execute error = %v, want it to mention %q", err, tc.wantErr)
			}
			if c.patches != 0 {
				t.Error("rolled back despite the refusal")
			}
		})
	}
}

// A ReplicaSet belonging to a different Deployment in the same namespace
// must not be mistaken for this one's history.
func TestDeploymentRollback_IgnoresAnotherDeploymentsReplicaSets(t *testing.T) {
	other := revisionSet("web-old", "9", "example/web:2.0")
	other.OwnerReferences[0].UID = "some-other-deployment"

	c := &rollbackClient{
		deployment: deployedAt("3"),
		replicaSets: []appsv1.ReplicaSet{
			other,
			revisionSet("api-old", "2", "example/api:1.4"),
		},
	}
	a := NewDeploymentRollback(c)

	if _, err := a.Execute(context.Background(), action.Request{Target: target}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(c.lastPatch, "example/web") {
		t.Errorf("patch = %s, want this Deployment's own history", c.lastPatch)
	}
}

func TestDeploymentRollback_Name(t *testing.T) {
	if got := NewDeploymentRollback(&rollbackClient{}).Name(); got != "deployment.rollback" {
		t.Errorf("Name() = %q, want deployment.rollback", got)
	}
}
