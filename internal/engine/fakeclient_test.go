package engine

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
)

// fakeClient is a small in-memory client.Client.
//
// controller-runtime ships a fake client, but writing this one keeps the
// test dependencies to what the operator already ships and, more usefully,
// makes every supported operation explicit: embedding client.Client means
// any call the reconciler makes that is not implemented here panics loudly
// instead of silently succeeding.
type fakeClient struct {
	client.Client

	mu      sync.Mutex
	objects map[string]client.Object
	nextID  int

	// Failure injection. Each hook, when set, replaces the operation.
	getErr          error
	createErr       error
	statusUpdateErr error
	listErr         error
	deleteErr       error

	// Call counters, for asserting what the reconciler actually did.
	statusUpdates int
	deletes       int

	// Now stamps creationTimestamp, as the API server does.
	Now func() time.Time
}

// now is the clock the fake stamps creations with. Tests that care inject one.
func (c *fakeClient) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return testClock
}

func newFakeClient(objs ...client.Object) *fakeClient {
	c := &fakeClient{objects: map[string]client.Object{}}
	for _, o := range objs {
		c.objects[keyOf(o)] = o.DeepCopyObject().(client.Object)
	}
	return c
}

func keyOf(o client.Object) string {
	return fmt.Sprintf("%T/%s/%s", o, o.GetNamespace(), o.GetName())
}

func keyFor(obj client.Object, key client.ObjectKey) string {
	return fmt.Sprintf("%T/%s/%s", obj, key.Namespace, key.Name)
}

func (c *fakeClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.getErr != nil {
		return c.getErr
	}
	stored, ok := c.objects[keyFor(obj, key)]
	if !ok {
		return apierrors.NewNotFound(
			schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "remediations"}, key.Name)
	}
	reflectCopy(stored, obj)
	return nil
}

func (c *fakeClient) List(_ context.Context, list client.ObjectList, opts ...client.ListOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.listErr != nil {
		return c.listErr
	}

	options := &client.ListOptions{}
	for _, o := range opts {
		o.ApplyToList(options)
	}

	switch typed := list.(type) {
	case *v1alpha1.RemediationList:
		typed.Items = nil
		for _, stored := range c.objects {
			rem, ok := stored.(*v1alpha1.Remediation)
			if !ok || !matchesOptions(rem, options) {
				continue
			}
			typed.Items = append(typed.Items, *rem.DeepCopy())
		}
	case *v1alpha1.RemediationStrategyList:
		typed.Items = nil
		for _, stored := range c.objects {
			strategy, ok := stored.(*v1alpha1.RemediationStrategy)
			if !ok {
				continue
			}
			typed.Items = append(typed.Items, *strategy.DeepCopy())
		}
	default:
		return fmt.Errorf("fakeClient: List does not support %T", list)
	}
	return nil
}

func matchesOptions(obj client.Object, options *client.ListOptions) bool {
	if options.Namespace != "" && obj.GetNamespace() != options.Namespace {
		return false
	}
	if options.LabelSelector != nil && !options.LabelSelector.Matches(labels.Set(obj.GetLabels())) {
		return false
	}
	return true
}

func (c *fakeClient) Create(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.createErr != nil {
		return c.createErr
	}
	if obj.GetName() == "" && obj.GetGenerateName() != "" {
		c.nextID++
		obj.SetName(obj.GetGenerateName() + strconv.Itoa(c.nextID))
	}
	// The API server stamps this, and code that reads it back — the give-up
	// guard asks whether it already gave up recently — was silently reading a
	// zero time from a fake that did not.
	if created := obj.GetCreationTimestamp(); created.IsZero() {
		obj.SetCreationTimestamp(metav1.NewTime(c.now()))
	}
	key := keyOf(obj)
	if _, exists := c.objects[key]; exists {
		return apierrors.NewAlreadyExists(
			schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "remediations"}, obj.GetName())
	}
	c.objects[key] = obj.DeepCopyObject().(client.Object)
	return nil
}

func (c *fakeClient) Delete(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.deletes++
	if c.deleteErr != nil {
		return c.deleteErr
	}
	delete(c.objects, keyOf(obj))
	return nil
}

func (c *fakeClient) Status() client.SubResourceWriter { return &fakeStatusWriter{client: c} }

type fakeStatusWriter struct {
	client.SubResourceWriter
	client *fakeClient
}

func (w *fakeStatusWriter) Update(
	_ context.Context, obj client.Object, _ ...client.SubResourceUpdateOption,
) error {
	w.client.mu.Lock()
	defer w.client.mu.Unlock()

	w.client.statusUpdates++
	if w.client.statusUpdateErr != nil {
		return w.client.statusUpdateErr
	}
	key := keyOf(obj)
	if _, ok := w.client.objects[key]; !ok {
		return apierrors.NewNotFound(
			schema.GroupResource{Group: v1alpha1.GroupVersion.Group, Resource: "remediations"}, obj.GetName())
	}
	w.client.objects[key] = obj.DeepCopyObject().(client.Object)
	return nil
}

// stored returns a deep copy of a Remediation held by the fake, so
// assertions cannot accidentally mutate it.
func (c *fakeClient) stored(namespace, name string) *v1alpha1.Remediation {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := fmt.Sprintf("%T/%s/%s", &v1alpha1.Remediation{}, namespace, name)
	if stored, ok := c.objects[key]; ok {
		return stored.(*v1alpha1.Remediation).DeepCopy()
	}
	return nil
}

func (c *fakeClient) remediations() []*v1alpha1.Remediation {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []*v1alpha1.Remediation
	for _, stored := range c.objects {
		if rem, ok := stored.(*v1alpha1.Remediation); ok {
			out = append(out, rem.DeepCopy())
		}
	}
	return out
}

func (c *fakeClient) counters() (statusUpdates, deletes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusUpdates, c.deletes
}

// reflectCopy copies a stored object into the caller's object.
func reflectCopy(from, into client.Object) {
	switch dst := into.(type) {
	case *v1alpha1.Remediation:
		*dst = *from.(*v1alpha1.Remediation).DeepCopy()
	case *v1alpha1.RemediationStrategy:
		*dst = *from.(*v1alpha1.RemediationStrategy).DeepCopy()
	default:
		panic(fmt.Sprintf("fakeClient: Get does not support %T", into))
	}
}

// Compile-time checks that the fake satisfies the interfaces it stands in for.
var (
	_ client.Client            = (*fakeClient)(nil)
	_ client.SubResourceWriter = (*fakeStatusWriter)(nil)
	_ types.NamespacedName     = types.NamespacedName{}
	_ runtime.Object           = (*v1alpha1.Remediation)(nil)
)
