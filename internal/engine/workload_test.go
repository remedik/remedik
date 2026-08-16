package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// objectReader serves whatever objects a test put in it, by kind and name.
type objectReader struct {
	client.Reader

	deployments  map[string]*appsv1.Deployment
	statefulSets map[string]*appsv1.StatefulSet
	daemonSets   map[string]*appsv1.DaemonSet
	replicaSets  map[string]*appsv1.ReplicaSet
	pods         map[string]*corev1.Pod

	err error
}

func (r *objectReader) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if r.err != nil {
		return r.err
	}
	missing := apierrors.NewNotFound(schema.GroupResource{}, key.Name)

	switch target := obj.(type) {
	case *appsv1.Deployment:
		found, ok := r.deployments[key.Name]
		if !ok {
			return missing
		}
		*target = *found
	case *appsv1.StatefulSet:
		found, ok := r.statefulSets[key.Name]
		if !ok {
			return missing
		}
		*target = *found
	case *appsv1.DaemonSet:
		found, ok := r.daemonSets[key.Name]
		if !ok {
			return missing
		}
		*target = *found
	case *appsv1.ReplicaSet:
		found, ok := r.replicaSets[key.Name]
		if !ok {
			return missing
		}
		*target = *found
	case *corev1.Pod:
		found, ok := r.pods[key.Name]
		if !ok {
			return missing
		}
		*target = *found
	default:
		return missing
	}
	return nil
}

func ownedBy(kind, name string) []metav1.OwnerReference {
	controller := true
	return []metav1.OwnerReference{{Kind: kind, Name: name, Controller: &controller}}
}

func replicas(n int32) *int32 { return &n }

func TestWorkloadHealth_ReadsEachWorkloadKind(t *testing.T) {
	reader := &objectReader{
		deployments: map[string]*appsv1.Deployment{"api": {
			ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "api"},
			Spec:       appsv1.DeploymentSpec{Replicas: replicas(4)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 3},
		}},
		statefulSets: map[string]*appsv1.StatefulSet{"pg": {
			ObjectMeta: metav1.ObjectMeta{Namespace: "db", Name: "pg"},
			Spec:       appsv1.StatefulSetSpec{Replicas: replicas(3)},
			Status:     appsv1.StatefulSetStatus{AvailableReplicas: 3},
		}},
		daemonSets: map[string]*appsv1.DaemonSet{"node-exporter": {
			ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "node-exporter"},
			// A DaemonSet counts nodes; the arithmetic is the same question
			// asked of a different denominator.
			Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 5, NumberAvailable: 4},
		}},
	}
	health := &WorkloadHealth{Reader: reader}

	tests := []struct {
		target        string
		wantDesired   int32
		wantAvailable int32
	}{
		{target: "deployment/payments/api", wantDesired: 4, wantAvailable: 3},
		{target: "statefulset/db/pg", wantDesired: 3, wantAvailable: 3},
		{target: "daemonset/kube-system/node-exporter", wantDesired: 5, wantAvailable: 4},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			got, applicable, err := health.Workload(context.Background(), tc.target)
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if !applicable {
				t.Fatal("applicable = false; this kind has a replica count")
			}
			if got.Desired != tc.wantDesired || got.Available != tc.wantAvailable {
				t.Errorf("workload = %d/%d, want %d/%d",
					got.Available, got.Desired, tc.wantAvailable, tc.wantDesired)
			}
			if got.Name != tc.target {
				t.Errorf("name = %q, want the target", got.Name)
			}
		})
	}
}

// pod.delete targets a pod, but the question the guard asks is about the
// thing behind it.
func TestWorkloadHealth_ResolvesAPodToItsWorkload(t *testing.T) {
	reader := &objectReader{
		pods: map[string]*corev1.Pod{
			"api-7d9f8-x2k1": {ObjectMeta: metav1.ObjectMeta{
				Namespace: "payments", Name: "api-7d9f8-x2k1",
				OwnerReferences: ownedBy("ReplicaSet", "api-7d9f8"),
			}},
			"pg-0": {ObjectMeta: metav1.ObjectMeta{
				Namespace: "db", Name: "pg-0",
				OwnerReferences: ownedBy("StatefulSet", "pg"),
			}},
			"orphan": {ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "orphan"}},
			"nightly-abc": {ObjectMeta: metav1.ObjectMeta{
				Namespace: "payments", Name: "nightly-abc",
				OwnerReferences: ownedBy("Job", "nightly"),
			}},
			"bare-rs-pod": {ObjectMeta: metav1.ObjectMeta{
				Namespace: "payments", Name: "bare-rs-pod",
				OwnerReferences: ownedBy("ReplicaSet", "standalone"),
			}},
		},
		replicaSets: map[string]*appsv1.ReplicaSet{
			"api-7d9f8": {
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "payments", Name: "api-7d9f8",
					OwnerReferences: ownedBy("Deployment", "api"),
				},
			},
			"standalone": {
				ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "standalone"},
				Spec:       appsv1.ReplicaSetSpec{Replicas: replicas(2)},
				Status:     appsv1.ReplicaSetStatus{AvailableReplicas: 2},
			},
		},
		deployments: map[string]*appsv1.Deployment{"api": {
			ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "api"},
			Spec:       appsv1.DeploymentSpec{Replicas: replicas(4)},
			Status:     appsv1.DeploymentStatus{AvailableReplicas: 4},
		}},
		statefulSets: map[string]*appsv1.StatefulSet{"pg": {
			ObjectMeta: metav1.ObjectMeta{Namespace: "db", Name: "pg"},
			Spec:       appsv1.StatefulSetSpec{Replicas: replicas(3)},
			Status:     appsv1.StatefulSetStatus{AvailableReplicas: 2},
		}},
	}
	health := &WorkloadHealth{Reader: reader}

	tests := []struct {
		name           string
		target         string
		wantApplicable bool
		wantName       string
		wantAvailable  int32
	}{
		{
			name: "through a ReplicaSet to its Deployment", target: "pod/payments/api-7d9f8-x2k1",
			wantApplicable: true, wantName: "deployment/payments/api", wantAvailable: 4,
		},
		{
			name: "straight to a StatefulSet", target: "pod/db/pg-0",
			wantApplicable: true, wantName: "statefulset/db/pg", wantAvailable: 2,
		},
		{
			// A bare ReplicaSet is a workload in its own right.
			name: "a ReplicaSet with no Deployment", target: "pod/payments/bare-rs-pod",
			wantApplicable: true, wantName: "replicaset/payments/standalone", wantAvailable: 2,
		},
		{
			// pod.delete refuses this on its own account; the guard simply
			// has nothing to measure.
			name: "a pod nothing owns", target: "pod/payments/orphan",
		},
		{
			name: "a Job's pod", target: "pod/payments/nightly-abc",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, applicable, err := health.Workload(context.Background(), tc.target)
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if applicable != tc.wantApplicable {
				t.Fatalf("applicable = %v, want %v", applicable, tc.wantApplicable)
			}
			if !tc.wantApplicable {
				return
			}
			if got.Name != tc.wantName {
				t.Errorf("name = %q, want %q", got.Name, tc.wantName)
			}
			if got.Available != tc.wantAvailable {
				t.Errorf("available = %d, want %d", got.Available, tc.wantAvailable)
			}
		})
	}
}

func TestWorkloadHealth_NothingToMeasure(t *testing.T) {
	health := &WorkloadHealth{Reader: &objectReader{}}

	for _, target := range []string{
		"",                           // an action that touches nothing
		"node/aks-np1-0003",          // no replica count
		"job/payments/nightly-28471", // not a replicated workload
		"nonsense",                   // unparseable
	} {
		t.Run(target, func(t *testing.T) {
			_, applicable, err := health.Workload(context.Background(), target)
			if err != nil {
				t.Fatalf("error = %v, want nil: not applicable is not a failure", err)
			}
			if applicable {
				t.Error("applicable = true; there is no workload here to measure")
			}
		})
	}
}

// A read that fails is not "nothing to measure": it is the guard being
// unable to evaluate, which must reach the caller as an error so the guard
// can refuse.
func TestWorkloadHealth_AFailedReadIsAnError(t *testing.T) {
	health := &WorkloadHealth{Reader: &objectReader{err: errors.New("forbidden")}}

	_, applicable, err := health.Workload(context.Background(), "deployment/payments/api")
	if err == nil {
		t.Fatal("error = nil; a failed read must not read as 'nothing to measure'")
	}
	if applicable {
		t.Error("applicable = true for a read that failed")
	}
	if !strings.Contains(err.Error(), "deployment/payments/api") {
		t.Errorf("error = %q, want it to name what could not be read", err)
	}
}

func TestWorkloadHealth_NoClientIsAnError(t *testing.T) {
	health := &WorkloadHealth{}

	if _, _, err := health.Workload(context.Background(), "deployment/payments/api"); err == nil {
		t.Fatal("error = nil; without a client the guard cannot evaluate")
	}
}
