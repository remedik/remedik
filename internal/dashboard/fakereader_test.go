package dashboard

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
)

// fakeReader is an in-memory stand-in for the manager's cache.
//
// It implements exactly the two methods the dashboard is given, which is
// the point: anything the handler tries to do beyond reading these two
// kinds does not compile, let alone run.
type fakeReader struct {
	remediations []v1alpha1.Remediation
	strategies   []v1alpha1.RemediationStrategy

	// listErr and getErr replace the operation, so the failure paths are
	// testable without a cluster.
	listErr error
	getErr  error

	// listedNamespaces records the namespace each List was scoped to, so a
	// test can assert the dashboard never reads outside its own.
	listedNamespaces []string
}

func (r *fakeReader) Get(
	_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption,
) error {
	if r.getErr != nil {
		return r.getErr
	}

	target, ok := obj.(*v1alpha1.Remediation)
	if !ok {
		return fmt.Errorf("fakeReader: unexpected type %T", obj)
	}
	for i := range r.remediations {
		rem := &r.remediations[i]
		if rem.Name == key.Name && rem.Namespace == key.Namespace {
			rem.DeepCopyInto(target)
			return nil
		}
	}
	return apierrors.NewNotFound(v1alpha1.Resource("remediations"), key.Name)
}

func (r *fakeReader) List(
	_ context.Context, list client.ObjectList, opts ...client.ListOption,
) error {
	if r.listErr != nil {
		return r.listErr
	}

	var options client.ListOptions
	for _, opt := range opts {
		opt.ApplyToList(&options)
	}
	r.listedNamespaces = append(r.listedNamespaces, options.Namespace)

	switch target := list.(type) {
	case *v1alpha1.RemediationList:
		target.Items = append([]v1alpha1.Remediation(nil), r.remediations...)
		return nil
	case *v1alpha1.RemediationStrategyList:
		target.Items = append([]v1alpha1.RemediationStrategy(nil), r.strategies...)
		return nil
	default:
		return fmt.Errorf("fakeReader: unexpected list type %T", list)
	}
}
