package workload

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

var podTarget = action.Target{Kind: "Pod", Namespace: "payments", Name: "api-7d9f8-x2k1"}

// podClient serves a scripted sequence of pod states and records evictions.
type podClient struct {
	client.Client

	states []*corev1.Pod
	gets   int

	evictErr    error
	evictions   int
	lastGrace   *int64
	lastEvicted string
}

func (c *podClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return notFound(key.Name)
	}
	if len(c.states) == 0 {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, key.Name)
	}
	index := min(c.gets, len(c.states)-1)
	c.gets++
	if c.states[index] == nil {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, key.Name)
	}
	*pod = *c.states[index]
	return nil
}

func (c *podClient) SubResource(name string) client.SubResourceClient {
	return &subResourceStub{parent: c, name: name}
}

// subResourceStub captures the eviction the action creates.
type subResourceStub struct {
	client.SubResourceClient

	parent *podClient
	name   string
}

func (s *subResourceStub) Create(
	_ context.Context, obj client.Object, subResource client.Object, _ ...client.SubResourceCreateOption,
) error {
	if s.name != "eviction" {
		return notFound(s.name)
	}
	s.parent.evictions++
	s.parent.lastEvicted = obj.GetNamespace() + "/" + obj.GetName()
	if eviction, ok := subResource.(*policyv1.Eviction); ok && eviction.DeleteOptions != nil {
		s.parent.lastGrace = eviction.DeleteOptions.GracePeriodSeconds
	}
	return s.parent.evictErr
}

func ownedPod(name string, uid types.UID) *corev1.Pod {
	controller := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "payments", Name: name, UID: uid,
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "ReplicaSet", Name: "api-7d9f8", Controller: &controller,
			}},
		},
		Spec:   corev1.PodSpec{NodeName: "node-3"},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func barePod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: name, UID: "bare-1"},
		Spec:       corev1.PodSpec{NodeName: "node-3"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func podDeleter(states ...*corev1.Pod) (*PodDelete, *podClient) {
	c := &podClient{states: states}
	a := NewPodDelete(c)
	a.poll = time.Millisecond
	return a, c
}

func TestPodDelete_Resolve(t *testing.T) {
	a := NewPodDelete(&podClient{})

	tests := []struct {
		name    string
		labels  map[string]string
		params  action.Params
		want    string
		wantErr string
	}{
		{
			name:   "from alert labels",
			labels: map[string]string{"namespace": "payments", "pod": "api-7d9f8-x2k1"},
			want:   "pod/payments/api-7d9f8-x2k1",
		},
		{
			name:   "step parameters win",
			labels: map[string]string{"namespace": "payments", "pod": "wrong"},
			params: action.Params{"pod": "api-7d9f8-x2k1"},
			want:   "pod/payments/api-7d9f8-x2k1",
		},
		{
			name:    "no pod label",
			labels:  map[string]string{"namespace": "payments"},
			wantErr: "no pod",
		},
		{
			name:    "no namespace",
			labels:  map[string]string{"pod": "api-7d9f8-x2k1"},
			wantErr: "no namespace",
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

func TestPodDelete_EvictsRatherThanDeletes(t *testing.T) {
	a, c := podDeleter(ownedPod("api-7d9f8-x2k1", "uid-1"))

	result, err := a.Execute(context.Background(), action.Request{Target: podTarget, Params: nil})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if c.evictions != 1 {
		t.Fatalf("created %d evictions, want 1", c.evictions)
	}
	if c.lastEvicted != "payments/api-7d9f8-x2k1" {
		t.Errorf("evicted %q, want payments/api-7d9f8-x2k1", c.lastEvicted)
	}
	// The whole point of the action: a caller reading this must be able to
	// tell that a PodDisruptionBudget was consulted.
	if !strings.Contains(result.Kubectl, "Eviction API") {
		t.Errorf("kubectl = %q, want it to note that this is an eviction", result.Kubectl)
	}
	if result.Outputs["uid"] != "uid-1" {
		t.Errorf("outputs = %v, want the evicted pod's UID recorded for verification", result.Outputs)
	}
	if result.Outputs["owner"] != "replicaset/api-7d9f8" {
		t.Errorf("outputs = %v, want the owner that will replace it", result.Outputs)
	}
}

func TestPodDelete_ADisruptionBudgetRefusal(t *testing.T) {
	a, c := podDeleter(ownedPod("api-7d9f8-x2k1", "uid-1"))
	c.evictErr = apierrors.NewTooManyRequests("cannot evict pod as it would violate the budget", 5)

	_, err := a.Execute(context.Background(), action.Request{Target: podTarget, Params: nil})
	if err == nil {
		t.Fatal("Execute() error = nil; a refused eviction is not a success")
	}
	// The message has to name the cause, because "429" means nothing to
	// somebody reading a Remediation at 3am.
	if !strings.Contains(err.Error(), "PodDisruptionBudget") {
		t.Errorf("error = %q, want it to name the disruption budget", err)
	}
	if !strings.Contains(err.Error(), "still running") {
		t.Errorf("error = %q, want it to say the pod was not touched", err)
	}
}

func TestPodDelete_RefusesAPodNothingWouldRecreate(t *testing.T) {
	a, c := podDeleter(barePod("standalone"))

	for _, call := range []struct {
		name string
		run  func() (action.Result, error)
	}{
		{"Plan", func() (action.Result, error) {
			return a.Plan(context.Background(), action.Request{Target: podTarget, Params: nil})
		}},
		{"Execute", func() (action.Result, error) {
			return a.Execute(context.Background(), action.Request{Target: podTarget, Params: nil})
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			_, err := call.run()
			if err == nil {
				t.Fatal("error = nil; deleting a pod nothing replaces is deletion, not remediation")
			}
			if !strings.Contains(err.Error(), "no controller owner") {
				t.Errorf("error = %q, want it to explain why", err)
			}
			if !strings.Contains(err.Error(), RequireOwnerParam) {
				t.Errorf("error = %q, want it to name the escape hatch", err)
			}
		})
	}

	if c.evictions != 0 {
		t.Errorf("evicted %d pods despite the refusal", c.evictions)
	}
}

func TestPodDelete_TheRefusalCanBeOverridden(t *testing.T) {
	a, c := podDeleter(barePod("standalone"))

	_, err := a.Execute(context.Background(), action.Request{Target: podTarget, Params: action.Params{RequireOwnerParam: "false"}})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil when the step accepts the risk", err)
	}
	if c.evictions != 1 {
		t.Errorf("created %d evictions, want 1", c.evictions)
	}
}

func TestPodDelete_GracePeriod(t *testing.T) {
	a, c := podDeleter(ownedPod("api-7d9f8-x2k1", "uid-1"))

	if _, err := a.Execute(context.Background(), action.Request{Target: podTarget, Params: action.Params{GracePeriodParam: "5"}}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if c.lastGrace == nil || *c.lastGrace != 5 {
		t.Errorf("grace period = %v, want 5", c.lastGrace)
	}

	a, _ = podDeleter(ownedPod("api-7d9f8-x2k1", "uid-1"))
	if _, err := a.Execute(context.Background(), action.Request{Target: podTarget, Params: action.Params{GracePeriodParam: "soon"}}); err == nil {
		t.Error("error = nil for an unparseable grace period; it must not silently become the default")
	}
}

func TestPodDelete_VerifyWaitsForThePodToGo(t *testing.T) {
	a, _ := podDeleter(
		ownedPod("api-7d9f8-x2k1", "uid-1"), // still there
		nil,                                 // gone
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := a.Verify(ctx, action.Request{Target: podTarget}, action.Result{Outputs: map[string]string{"uid": "uid-1"}})
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}
	if result.Outputs["outcome"] != "deleted" {
		t.Errorf("outputs = %v, want outcome deleted", result.Outputs)
	}
}

// A StatefulSet replaces a pod with one of the same name. Only the UID
// distinguishes the replacement from the original still terminating.
func TestPodDelete_VerifyAcceptsAReplacementWithTheSameName(t *testing.T) {
	a, _ := podDeleter(
		ownedPod("api-7d9f8-x2k1", "uid-1"),
		ownedPod("api-7d9f8-x2k1", "uid-2"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := a.Verify(ctx, action.Request{Target: podTarget}, action.Result{Outputs: map[string]string{"uid": "uid-1"}})
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}
	if result.Outputs["outcome"] != "replaced" {
		t.Errorf("outputs = %v, want outcome replaced", result.Outputs)
	}
	if !strings.Contains(result.Summary, "uid-2") {
		t.Errorf("summary = %q, want it to name the new pod", result.Summary)
	}
}

func TestPodDelete_VerifyFailsOnAPodStuckTerminating(t *testing.T) {
	stuck := ownedPod("api-7d9f8-x2k1", "uid-1")
	deleting := metav1.NewTime(fixedClock)
	stuck.DeletionTimestamp = &deleting

	a, _ := podDeleter(stuck)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	result, err := a.Verify(ctx, action.Request{Target: podTarget}, action.Result{Outputs: map[string]string{"uid": "uid-1"}})
	if err == nil {
		t.Fatal("Verify() error = nil; a pod that never went away is not a completed remediation")
	}
	if !strings.Contains(result.Summary, "terminating") {
		t.Errorf("summary = %q, want it to say the pod is still terminating", result.Summary)
	}
}

func TestPodDelete_MissingPod(t *testing.T) {
	a, _ := podDeleter()

	if _, err := a.Execute(context.Background(), action.Request{Target: podTarget, Params: nil}); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %v, want a not-found error", err)
	}
}

func TestPodDelete_Name(t *testing.T) {
	if got := NewPodDelete(&podClient{}).Name(); got != "pod.delete" {
		t.Errorf("Name() = %q, want pod.delete", got)
	}
}
