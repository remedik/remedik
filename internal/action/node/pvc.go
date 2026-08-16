package node

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/internal/action"
)

// Parameters of pvc.expand.
const (
	// SizeParam sets an absolute size, such as "50Gi".
	SizeParam = "size"
	// IncreasePercentParam grows the claim by a share of its current size.
	IncreasePercentParam = "increasePercent"
	// MaxSizeParam is the ceiling. Required for a relative increase, for the
	// same reason the scaling actions require one: growth with no limit is
	// a bill nobody agreed to.
	MaxSizeParam = "maxSize"
)

// LabelPVC is the alert label naming the claim.
const LabelPVC = "persistentvolumeclaim"

// LabelNamespace is the alert label naming its namespace.
const LabelNamespace = "namespace"

// PVCExpand grows a PersistentVolumeClaim.
//
// The whole value of the action is one check: where the StorageClass does
// not set `allowVolumeExpansion`, the API server accepts the patch and
// nothing happens. A remediation that reports success and changes nothing is
// the worst outcome available — worse than failing, because nobody goes
// looking. So the check comes first and the refusal names the StorageClass.
type PVCExpand struct {
	client client.Client
	poll   time.Duration
}

// NewPVCExpand builds the action.
func NewPVCExpand(c client.Client) *PVCExpand {
	return &PVCExpand{client: c, poll: DefaultPoll}
}

// Name implements action.Action.
func (a *PVCExpand) Name() string { return "pvc.expand" }

// Resolve determines the claim from the alert's labels.
func (a *PVCExpand) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	namespace := params.Get(LabelNamespace, labels[LabelNamespace])
	name := params.Get(NameParam, params.Get(LabelPVC, labels[LabelPVC]))

	if namespace == "" {
		return action.Target{}, fmt.Errorf(
			"no namespace: the alert has no %q label and the step sets no %q parameter",
			LabelNamespace, LabelNamespace)
	}
	if name == "" {
		return action.Target{}, fmt.Errorf(
			"no claim: the alert has no %q label and the step sets no %q parameter",
			LabelPVC, NameParam)
	}
	return action.Target{Kind: "PersistentVolumeClaim", Namespace: namespace, Name: name}, nil
}

// Plan reports the change, performing every check Execute performs.
func (a *PVCExpand) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	claim, want, err := a.plan(ctx, req)
	if err != nil {
		return action.Result{}, err
	}
	current := requested(claim)

	result := action.Result{
		Summary: fmt.Sprintf("expand %s from %s to %s", req.Target, current.String(), want.String()),
		Kubectl: kubectlExpand(req.Target, want.String()),
	}
	result.Output("sizeBefore", current.String())
	result.Output("sizeAfter", want.String())
	return result, nil
}

// Execute raises the claim's request.
func (a *PVCExpand) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	claim, want, err := a.plan(ctx, req)
	if err != nil {
		return action.Result{}, err
	}
	current := requested(claim)

	patch := []byte(fmt.Sprintf(
		`{"spec":{"resources":{"requests":{"storage":%q}}}}`, want.String()))
	if err := a.client.Patch(ctx, claim, client.RawPatch(mergePatch, patch)); err != nil {
		if apierrors.IsForbidden(err) {
			return action.Result{}, fmt.Errorf("not permitted to patch %s: %w", req.Target, err)
		}
		return action.Result{}, fmt.Errorf("expand %s: %w", req.Target, err)
	}

	result := action.Result{
		Summary: fmt.Sprintf("expanded %s from %s to %s; expansion is one-way, Kubernetes cannot shrink it back",
			req.Target, current.String(), want.String()),
		Kubectl: kubectlExpand(req.Target, want.String()),
	}
	result.Output("sizeBefore", current.String())
	result.Output("sizeAfter", want.String())
	return result, nil
}

// Verify waits for the claim to report the new capacity.
//
// The request being accepted is not the expansion happening: the CSI driver
// resizes the volume, and some need the pod to restart before the filesystem
// follows. Status capacity is the number that means storage actually
// arrived.
func (a *PVCExpand) Verify(
	ctx context.Context, req action.Request, executed action.Result,
) (action.Result, error) {
	ctx, cancel := action.WithVerifyDeadline(ctx)
	defer cancel()

	want, err := resource.ParseQuantity(executed.Outputs["sizeAfter"])
	if err != nil {
		return action.Result{}, fmt.Errorf("no target size was recorded to verify")
	}

	var last string
	for {
		claim, fetchErr := a.fetch(ctx, req.Target)
		if fetchErr != nil {
			return action.Result{Summary: last}, fetchErr
		}

		capacity := claim.Status.Capacity[corev1.ResourceStorage]
		last = fmt.Sprintf("%s reports %s of %s", req.Target, capacity.String(), want.String())
		if capacity.Cmp(want) >= 0 {
			result := action.Result{Summary: fmt.Sprintf("%s is now %s", req.Target, capacity.String())}
			result.Output("capacity", capacity.String())
			return result, nil
		}

		select {
		case <-ctx.Done():
			return action.Result{Summary: last},
				fmt.Errorf("the volume did not report the new size in time: %s. "+
					"Some CSI drivers finish the filesystem resize only when the pod restarts", last)
		case <-time.After(a.poll):
		}
	}
}

func (a *PVCExpand) plan(
	ctx context.Context, req action.Request,
) (*corev1.PersistentVolumeClaim, resource.Quantity, error) {
	claim, err := a.fetch(ctx, req.Target)
	if err != nil {
		return nil, resource.Quantity{}, err
	}

	if err := a.checkExpandable(ctx, claim, req.Target); err != nil {
		return nil, resource.Quantity{}, err
	}

	want, err := targetSize(req.Params, requested(claim))
	if err != nil {
		return nil, resource.Quantity{}, err
	}
	return claim, want, nil
}

// checkExpandable refuses a claim whose StorageClass will not grow.
func (a *PVCExpand) checkExpandable(
	ctx context.Context, claim *corev1.PersistentVolumeClaim, target action.Target,
) error {
	name := ""
	if claim.Spec.StorageClassName != nil {
		name = *claim.Spec.StorageClassName
	}
	if name == "" {
		return fmt.Errorf(
			"%s names no StorageClass, so remedik cannot tell whether it may be expanded",
			target)
	}

	var class storagev1.StorageClass
	if err := a.client.Get(ctx, client.ObjectKey{Name: name}, &class); err != nil {
		return fmt.Errorf("read storageclass/%s to check whether %s may be expanded: %w",
			name, target, err)
	}

	if class.AllowVolumeExpansion == nil || !*class.AllowVolumeExpansion {
		return fmt.Errorf(
			"storageclass/%s does not set allowVolumeExpansion, so %s cannot grow: "+
				"the API server would accept the change and nothing would happen, and remedik "+
				"would have recorded a success that did nothing",
			name, target)
	}
	return nil
}

func (a *PVCExpand) fetch(
	ctx context.Context, target action.Target,
) (*corev1.PersistentVolumeClaim, error) {
	var claim corev1.PersistentVolumeClaim
	key := client.ObjectKey{Namespace: target.Namespace, Name: target.Name}

	if err := a.client.Get(ctx, key, &claim); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return nil, fmt.Errorf("%s does not exist: %w", target, err)
		case apierrors.IsForbidden(err):
			return nil, fmt.Errorf("not permitted to read %s: %w", target, err)
		default:
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
	}
	return &claim, nil
}

func requested(claim *corev1.PersistentVolumeClaim) resource.Quantity {
	return claim.Spec.Resources.Requests[corev1.ResourceStorage]
}

// targetSize works out how big the claim should become.
func targetSize(params action.Params, current resource.Quantity) (resource.Quantity, error) {
	absolute := params.Get(SizeParam, "")
	percent := params.Get(IncreasePercentParam, "")

	switch {
	case absolute != "" && percent != "":
		return resource.Quantity{}, fmt.Errorf(
			"the step sets both %q and %q; they mean different things", SizeParam, IncreasePercentParam)
	case absolute == "" && percent == "":
		return resource.Quantity{}, fmt.Errorf(
			"the step must set %q or %q", SizeParam, IncreasePercentParam)
	}

	if absolute != "" {
		want, err := resource.ParseQuantity(absolute)
		if err != nil {
			return resource.Quantity{}, fmt.Errorf(
				"parameter %q: %q is not a quantity such as \"50Gi\"", SizeParam, absolute)
		}
		return checkGrowth(want, current)
	}

	share, err := parsePercent(percent)
	if err != nil {
		return resource.Quantity{}, err
	}
	ceiling, err := resource.ParseQuantity(params.Get(MaxSizeParam, ""))
	if err != nil {
		return resource.Quantity{}, fmt.Errorf(
			"a relative increase needs a ceiling: parameter %q must be a quantity such as \"200Gi\"",
			MaxSizeParam)
	}

	// Quantities are exact; do the arithmetic in bytes and round to a whole
	// mebibyte so the result reads like a size somebody would type.
	const mebibyte = 1 << 20
	grown := current.Value() + current.Value()*int64(share)/100
	grown = ((grown + mebibyte - 1) / mebibyte) * mebibyte
	want := *resource.NewQuantity(grown, resource.BinarySI)

	if want.Cmp(ceiling) > 0 {
		want = ceiling
	}
	return checkGrowth(want, current)
}

// checkGrowth refuses anything that is not an increase. Kubernetes cannot
// shrink a volume, so asking is a mistake to report rather than a request to
// attempt.
func checkGrowth(want, current resource.Quantity) (resource.Quantity, error) {
	if want.Cmp(current) <= 0 {
		return resource.Quantity{}, fmt.Errorf(
			"the claim already requests %s and this step asks for %s: Kubernetes cannot shrink a "+
				"volume, and there is nothing to do when it is already large enough",
			current.String(), want.String())
	}
	return want, nil
}

// parsePercent is strict on purpose. Sscanf would read "50abc" as 50 and
// drop the rest, so a typo would become a remediation nobody wrote.
func parsePercent(raw string) (int, error) {
	share, err := strconv.Atoi(raw)
	if err != nil || share <= 0 {
		return 0, fmt.Errorf("parameter %q: %q is not a positive percentage",
			IncreasePercentParam, raw)
	}
	return share, nil
}

func kubectlExpand(target action.Target, size string) string {
	return fmt.Sprintf(
		`kubectl patch pvc %s -n %s --type=merge -p '{"spec":{"resources":{"requests":{"storage":"%s"}}}}'`,
		target.Name, target.Namespace, size)
}

// Compile-time proof that the action satisfies the contract.
var (
	_ action.Action   = (*PVCExpand)(nil)
	_ action.Verifier = (*PVCExpand)(nil)
)
