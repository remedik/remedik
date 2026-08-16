package node

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

var nodeTarget = action.Target{Kind: "Node", Name: "aks-np1-0003"}

// clusterClient serves nodes, pods, claims and storage classes, and records
// what was patched or evicted.
type clusterClient struct {
	client.Client

	node         *corev1.Node
	pods         []corev1.Pod
	claim        *corev1.PersistentVolumeClaim
	storageClass *storagev1.StorageClass

	listErr error

	patches   int
	lastPatch string

	evictions int
	evicted   []string
	// refuseUntil makes the first N eviction attempts answer 429, the way a
	// PodDisruptionBudget does partway through a drain.
	refuseUntil int
	evictErr    error
}

func (c *clusterClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	missing := apierrors.NewNotFound(schema.GroupResource{}, key.Name)

	switch target := obj.(type) {
	case *corev1.Node:
		if c.node == nil {
			return missing
		}
		*target = *c.node
	case *corev1.PersistentVolumeClaim:
		if c.claim == nil {
			return missing
		}
		*target = *c.claim
	case *storagev1.StorageClass:
		if c.storageClass == nil || c.storageClass.Name != key.Name {
			return missing
		}
		*target = *c.storageClass
	default:
		return missing
	}
	return nil
}

func (c *clusterClient) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	if c.listErr != nil {
		return c.listErr
	}
	pods, ok := list.(*corev1.PodList)
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{}, "")
	}
	pods.Items = append([]corev1.Pod(nil), c.pods...)
	return nil
}

func (c *clusterClient) Patch(_ context.Context, obj client.Object, patch client.Patch, _ ...client.PatchOption) error {
	data, _ := patch.Data(obj)
	c.patches++
	c.lastPatch = string(data)

	// Reflect the change, so verification sees what a real cluster would.
	if node, ok := obj.(*corev1.Node); ok && c.node != nil {
		c.node.Spec.Unschedulable = strings.Contains(c.lastPatch, "true")
		_ = node
	}
	return nil
}

func (c *clusterClient) SubResource(name string) client.SubResourceClient {
	return &evictionStub{parent: c, name: name}
}

type evictionStub struct {
	client.SubResourceClient

	parent *clusterClient
	name   string
}

func (e *evictionStub) Create(
	_ context.Context, obj client.Object, subResource client.Object, _ ...client.SubResourceCreateOption,
) error {
	if e.name != "eviction" {
		return apierrors.NewNotFound(schema.GroupResource{}, e.name)
	}
	if _, ok := subResource.(*policyv1.Eviction); !ok {
		return apierrors.NewBadRequest("not an eviction")
	}

	e.parent.evictions++
	if e.parent.evictErr != nil {
		return e.parent.evictErr
	}
	if e.parent.refuseUntil > 0 {
		e.parent.refuseUntil--
		return apierrors.NewTooManyRequests("the disruption budget does not allow it yet", 5)
	}

	name := obj.GetNamespace() + "/" + obj.GetName()
	e.parent.evicted = append(e.parent.evicted, name)
	// The pod is gone; drop it so a re-list sees an emptier node.
	remaining := e.parent.pods[:0]
	for _, pod := range e.parent.pods {
		if pod.Namespace+"/"+pod.Name != name {
			remaining = append(remaining, pod)
		}
	}
	e.parent.pods = remaining
	return nil
}

func aNode(name string, unschedulable bool) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: unschedulable},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		}},
	}
}

type podOption func(*corev1.Pod)

func ownedByKind(kind string) podOption {
	return func(p *corev1.Pod) {
		controller := true
		p.OwnerReferences = []metav1.OwnerReference{{
			Kind: kind, Name: "owner", Controller: &controller,
		}}
	}
}

func withEmptyDir() podOption {
	return func(p *corev1.Pod) {
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{
			Name:         "scratch",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		})
	}
}

func mirrorPod() podOption {
	return func(p *corev1.Pod) {
		p.Annotations = map[string]string{mirrorPodAnnotation: "abc"}
	}
}

func finished() podOption {
	return func(p *corev1.Pod) { p.Status.Phase = corev1.PodSucceeded }
}

func aPod(namespace, name string, opts ...podOption) corev1.Pod {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec:       corev1.PodSpec{NodeName: nodeTarget.Name},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	// Most pods have a controller; the tests that care say otherwise.
	ownedByKind("ReplicaSet")(&pod)
	for _, opt := range opts {
		opt(&pod)
	}
	return pod
}

func nodeRequest(params action.Params) action.Request {
	return action.Request{Target: nodeTarget, Params: params}
}

// --------------------------------------------------------------------------
// cordon and uncordon
// --------------------------------------------------------------------------

func TestCordon_MarksTheNodeUnschedulable(t *testing.T) {
	c := &clusterClient{node: aNode(nodeTarget.Name, false)}
	a := NewCordon(c)

	result, err := a.Execute(context.Background(), nodeRequest(nil))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if c.lastPatch != `{"spec":{"unschedulable":true}}` {
		t.Errorf("patch = %s", c.lastPatch)
	}
	if result.Outputs["changed"] != "true" {
		t.Errorf("outputs = %v, want changed", result.Outputs)
	}
	if !strings.Contains(result.Kubectl, "kubectl cordon") {
		t.Errorf("kubectl = %q", result.Kubectl)
	}
}

// An alert fires repeatedly. Reporting "already cordoned" as a failure would
// make the strategy unusable on the second firing, which is every firing.
func TestCordon_IsIdempotent(t *testing.T) {
	c := &clusterClient{node: aNode(nodeTarget.Name, true)}
	a := NewCordon(c)

	result, err := a.Execute(context.Background(), nodeRequest(nil))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil for an already-cordoned node", err)
	}
	if c.patches != 0 {
		t.Errorf("patched %d times for a node already in the wanted state", c.patches)
	}
	if result.Outputs["changed"] != "false" {
		t.Errorf("outputs = %v, want changed=false", result.Outputs)
	}
}

func TestUncordon_ClearsIt(t *testing.T) {
	c := &clusterClient{node: aNode(nodeTarget.Name, true)}
	a := NewUncordon(c)

	if _, err := a.Execute(context.Background(), nodeRequest(nil)); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if c.lastPatch != `{"spec":{"unschedulable":false}}` {
		t.Errorf("patch = %s", c.lastPatch)
	}
	if a.Name() != "node.uncordon" {
		t.Errorf("Name() = %q", a.Name())
	}
}

func TestCordon_Resolve(t *testing.T) {
	a := NewCordon(&clusterClient{})

	got, err := a.Resolve(map[string]string{"node": "aks-np1-0003"}, nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	// Nodes are cluster-scoped: the target must carry no namespace.
	if got.String() != "node/aks-np1-0003" {
		t.Errorf("target = %q", got.String())
	}
	if _, err := a.Resolve(nil, nil); err == nil {
		t.Error("error = nil for an alert naming no node")
	}
}

// --------------------------------------------------------------------------
// drain
// --------------------------------------------------------------------------

func TestDrain_EvictsWhatItShouldAndSkipsWhatItShouldNot(t *testing.T) {
	c := &clusterClient{
		node: aNode(nodeTarget.Name, false),
		pods: []corev1.Pod{
			aPod("payments", "api-1"),
			aPod("kube-system", "node-exporter-x", ownedByKind("DaemonSet")),
			aPod("kube-system", "kube-apiserver", mirrorPod()),
			aPod("payments", "orphan", func(p *corev1.Pod) { p.OwnerReferences = nil }),
			aPod("payments", "cache-1", withEmptyDir()),
			aPod("payments", "done", finished()),
		},
	}
	a := NewDrain(c)
	a.poll = time.Millisecond

	result, err := a.Execute(context.Background(), nodeRequest(nil))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Only the ordinary pod moves.
	if len(c.evicted) != 1 || c.evicted[0] != "payments/api-1" {
		t.Fatalf("evicted %v, want only payments/api-1", c.evicted)
	}
	if result.Outputs["skipped"] != "5" {
		t.Errorf("outputs = %v, want 5 skipped", result.Outputs)
	}
	// Draining without cordoning is a race against the scheduler.
	if c.patches != 1 || !strings.Contains(c.lastPatch, "true") {
		t.Errorf("the node was not cordoned first: %d patches, last %s", c.patches, c.lastPatch)
	}
}

// During a drain, a 429 means "not yet" rather than "no": kubectl drain
// retries, and so does this.
func TestDrain_RetriesADisruptionBudgetRefusal(t *testing.T) {
	c := &clusterClient{
		node:        aNode(nodeTarget.Name, false),
		pods:        []corev1.Pod{aPod("payments", "api-1")},
		refuseUntil: 2,
	}
	a := NewDrain(c)
	a.poll = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := a.Execute(ctx, nodeRequest(nil)); err != nil {
		t.Fatalf("Execute() error = %v, want the refusals to be retried", err)
	}
	if c.evictions != 3 {
		t.Errorf("attempted %d evictions, want 3 (two refusals then success)", c.evictions)
	}
	if len(c.evicted) != 1 {
		t.Errorf("evicted %v, want the pod to have gone eventually", c.evicted)
	}
}

// Half-drained is the worst state to leave a node in, so it is reported as
// the failure it is.
func TestDrain_APartialDrainIsAFailure(t *testing.T) {
	c := &clusterClient{
		node:        aNode(nodeTarget.Name, false),
		pods:        []corev1.Pod{aPod("payments", "api-1"), aPod("payments", "api-2")},
		refuseUntil: 1000, // the budget never allows it
	}
	a := NewDrain(c)
	a.poll = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	result, err := a.Execute(ctx, nodeRequest(nil))
	if err == nil {
		t.Fatal("Execute() error = nil; a node that is only partly drained is not drained")
	}
	if !strings.Contains(err.Error(), "PodDisruptionBudget") {
		t.Errorf("error = %q, want it to name the cause", err)
	}
	// The node stays cordoned: uncordoning one somebody is mid-way through
	// draining would be worse than leaving it.
	if !strings.Contains(err.Error(), "stays cordoned") {
		t.Errorf("error = %q, want it to say the node is still cordoned", err)
	}
	if result.Outputs["remaining"] == "" {
		t.Errorf("outputs = %v, want the pods still there", result.Outputs)
	}
}

func TestDrain_PlanNamesWhatWouldMove(t *testing.T) {
	c := &clusterClient{
		node: aNode(nodeTarget.Name, false),
		pods: []corev1.Pod{
			aPod("payments", "api-1"),
			aPod("payments", "api-2"),
			aPod("kube-system", "node-exporter-x", ownedByKind("DaemonSet")),
		},
	}
	a := NewDrain(c)

	result, err := a.Plan(context.Background(), nodeRequest(nil))
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if c.evictions != 0 || c.patches != 0 {
		t.Fatal("Plan changed the cluster")
	}
	// The dry-run report is the list somebody should read before allowing a
	// drain unattended, so it names the pods rather than counting them.
	if !strings.Contains(result.Outputs["pods"], "payments/api-1") {
		t.Errorf("outputs = %v, want the pods named", result.Outputs)
	}
	if result.Outputs["podsToEvict"] != "2" {
		t.Errorf("outputs = %v, want 2 evictable", result.Outputs)
	}
	if !strings.Contains(result.Summary, "DaemonSet") {
		t.Errorf("summary = %q, want it to say what is skipped and why", result.Summary)
	}
}

func TestDrain_RefusesMoreThanTheStepAllows(t *testing.T) {
	c := &clusterClient{
		node: aNode(nodeTarget.Name, false),
		pods: []corev1.Pod{aPod("payments", "a"), aPod("payments", "b"), aPod("payments", "c")},
	}
	a := NewDrain(c)

	_, err := a.Execute(context.Background(), nodeRequest(action.Params{MaxPodsParam: "2"}))
	if err == nil {
		t.Fatal("error = nil; the node holds more pods than the step allows")
	}
	if c.evictions != 0 || c.patches != 0 {
		t.Error("drained despite the refusal")
	}
}

func TestDrain_EmptyDirAndBarePodsCanBeOptedIn(t *testing.T) {
	c := &clusterClient{
		node: aNode(nodeTarget.Name, false),
		pods: []corev1.Pod{
			aPod("payments", "cache-1", withEmptyDir()),
			aPod("payments", "orphan", func(p *corev1.Pod) { p.OwnerReferences = nil }),
		},
	}
	a := NewDrain(c)
	a.poll = time.Millisecond

	if _, err := a.Execute(context.Background(), nodeRequest(action.Params{
		DeleteEmptyDirParam: "true",
		EvictBarePodsParam:  "true",
	})); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(c.evicted) != 2 {
		t.Errorf("evicted %v, want both once the step accepted the risk", c.evicted)
	}
}

// --------------------------------------------------------------------------
// pvc.expand
// --------------------------------------------------------------------------

func expandable(allow bool) *storagev1.StorageClass {
	return &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: "fast"},
		AllowVolumeExpansion: &allow,
	}
}

func aClaim(request, capacity string) *corev1.PersistentVolumeClaim {
	class := "fast"
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "data"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &class,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(request)},
			},
		},
	}
	if capacity != "" {
		claim.Status.Capacity = corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(capacity)}
	}
	return claim
}

var claimTarget = action.Target{Kind: "PersistentVolumeClaim", Namespace: "payments", Name: "data"}

func TestPVCExpand_GrowsTheClaim(t *testing.T) {
	c := &clusterClient{claim: aClaim("10Gi", "10Gi"), storageClass: expandable(true)}
	a := NewPVCExpand(c)

	result, err := a.Execute(context.Background(), action.Request{
		Target: claimTarget, Params: action.Params{SizeParam: "20Gi"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(c.lastPatch, "20Gi") {
		t.Errorf("patch = %s", c.lastPatch)
	}
	// Expansion is one-way; the record says so, because nobody reads the
	// docs during an incident.
	if !strings.Contains(result.Summary, "cannot shrink") {
		t.Errorf("summary = %q, want it to say expansion is one-way", result.Summary)
	}
}

// The whole value of the action: where the StorageClass does not allow it,
// the API accepts the patch and nothing happens.
func TestPVCExpand_RefusesAStorageClassThatCannotGrow(t *testing.T) {
	c := &clusterClient{claim: aClaim("10Gi", "10Gi"), storageClass: expandable(false)}
	a := NewPVCExpand(c)

	for _, call := range []struct {
		name string
		run  func() (action.Result, error)
	}{
		{"Plan", func() (action.Result, error) {
			return a.Plan(context.Background(), action.Request{
				Target: claimTarget, Params: action.Params{SizeParam: "20Gi"}})
		}},
		{"Execute", func() (action.Result, error) {
			return a.Execute(context.Background(), action.Request{
				Target: claimTarget, Params: action.Params{SizeParam: "20Gi"}})
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			_, err := call.run()
			if err == nil {
				t.Fatal("error = nil; this would have recorded a success that did nothing")
			}
			if !strings.Contains(err.Error(), "allowVolumeExpansion") {
				t.Errorf("error = %q, want it to name the reason", err)
			}
		})
	}
	if c.patches != 0 {
		t.Error("patched despite the refusal")
	}
}

func TestPVCExpand_NeverShrinks(t *testing.T) {
	c := &clusterClient{claim: aClaim("50Gi", "50Gi"), storageClass: expandable(true)}
	a := NewPVCExpand(c)

	_, err := a.Execute(context.Background(), action.Request{
		Target: claimTarget, Params: action.Params{SizeParam: "10Gi"},
	})
	if err == nil {
		t.Fatal("error = nil; Kubernetes cannot shrink a volume")
	}
	if c.patches != 0 {
		t.Error("patched despite the refusal")
	}
}

func TestTargetSize(t *testing.T) {
	tests := []struct {
		name    string
		params  action.Params
		current string
		want    string
		wantErr string
	}{
		{
			name:    "absolute",
			params:  action.Params{SizeParam: "20Gi"},
			current: "10Gi", want: "20Gi",
		},
		{
			name:    "relative",
			params:  action.Params{IncreasePercentParam: "50", MaxSizeParam: "100Gi"},
			current: "10Gi", want: "15Gi",
		},
		{
			name:    "relative, capped by the ceiling",
			params:  action.Params{IncreasePercentParam: "500", MaxSizeParam: "20Gi"},
			current: "10Gi", want: "20Gi",
		},
		{
			// Growth with no limit is a bill nobody agreed to.
			name:    "relative with no ceiling",
			params:  action.Params{IncreasePercentParam: "50"},
			current: "10Gi", wantErr: "needs a ceiling",
		},
		{
			name:    "both",
			params:  action.Params{SizeParam: "20Gi", IncreasePercentParam: "50"},
			current: "10Gi", wantErr: "different things",
		},
		{
			name:    "neither",
			params:  action.Params{},
			current: "10Gi", wantErr: "must set",
		},
		{
			name:    "not a quantity",
			params:  action.Params{SizeParam: "lots"},
			current: "10Gi", wantErr: "not a quantity",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := targetSize(tc.params, resource.MustParse(tc.current))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			want := resource.MustParse(tc.want)
			if got.Cmp(want) != 0 {
				t.Errorf("size = %s, want %s", got.String(), want.String())
			}
		})
	}
}

// The request being accepted is not the expansion happening: the CSI driver
// does the work, and status capacity is what says storage arrived.
func TestPVCExpand_VerifyWaitsForTheCapacity(t *testing.T) {
	c := &clusterClient{claim: aClaim("20Gi", "20Gi"), storageClass: expandable(true)}
	a := NewPVCExpand(c)
	a.poll = time.Millisecond

	executed := action.Result{Outputs: map[string]string{"sizeAfter": "20Gi"}}
	if _, err := a.Verify(context.Background(), action.Request{Target: claimTarget}, executed); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	// Now one where the driver never finishes.
	c.claim = aClaim("20Gi", "10Gi")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := a.Verify(ctx, action.Request{Target: claimTarget}, executed)
	if err == nil {
		t.Fatal("Verify() error = nil; the volume never reported the new size")
	}
	if !strings.Contains(err.Error(), "pod restarts") {
		t.Errorf("error = %q, want it to explain the common cause", err)
	}
}
