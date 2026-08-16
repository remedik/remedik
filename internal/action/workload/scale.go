package workload

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

// mergePatch is the content type for the JSON merge patches these actions
// send. Strategic merge would work too; merge is enough for a scalar and
// says less about how the server should combine lists.
const mergePatch = types.MergePatchType

// Parameters shared by the scaling actions.
const (
	// ReplicasParam sets an absolute count.
	ReplicasParam = "replicas"
	// IncreaseByParam adds to the current count.
	IncreaseByParam = "increaseBy"
	// IncreasePercentParam adds a share of the current count, rounded up.
	IncreasePercentParam = "increasePercent"
	// MaxParam is the ceiling. Required whenever the change is relative:
	// "increase by" with no ceiling is an alert storm with a credit card.
	MaxParam = "max"
)

// DeploymentScale changes how many replicas a Deployment runs.
//
// It refuses a workload a HorizontalPodAutoscaler owns. Setting replicas on
// one is a change the autoscaler reverts on its next interval, so the
// remediation records success and nothing sticks — the specific failure that
// teaches people not to trust an automation.
type DeploymentScale struct {
	client client.Client
	poll   time.Duration
}

// NewDeploymentScale builds the action.
func NewDeploymentScale(c client.Client) *DeploymentScale {
	return &DeploymentScale{client: c, poll: DefaultVerifyPoll}
}

// Name implements action.Action.
func (a *DeploymentScale) Name() string { return "deployment.scale" }

// Resolve determines the Deployment.
func (a *DeploymentScale) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	return (&Restart{pinnedKind: "Deployment"}).Resolve(labels, params)
}

// Plan reports the change and performs every check Execute performs.
func (a *DeploymentScale) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	deployment, want, err := a.plan(ctx, req)
	if err != nil {
		return action.Result{}, err
	}
	current := readRollout(deployment).desired

	result := action.Result{
		Summary: fmt.Sprintf("scale %s from %d to %d replicas", req.Target, current, want),
		Kubectl: kubectlScale(req.Target, want),
	}
	result.Output("replicasBefore", strconv.Itoa(int(current)))
	result.Output("replicasAfter", strconv.Itoa(int(want)))
	return result, nil
}

// Execute writes the new count through the scale subresource.
func (a *DeploymentScale) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	deployment, want, err := a.plan(ctx, req)
	if err != nil {
		return action.Result{}, err
	}
	current := readRollout(deployment).desired

	patch := []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, want))
	if err := a.client.SubResource("scale").Patch(ctx, deployment,
		client.RawPatch(mergePatch, patch)); err != nil {
		if apierrors.IsForbidden(err) {
			return action.Result{}, fmt.Errorf("not permitted to scale %s: %w", req.Target, err)
		}
		return action.Result{}, fmt.Errorf("scale %s: %w", req.Target, err)
	}

	result := action.Result{
		Summary: fmt.Sprintf("scaled %s from %d to %d replicas", req.Target, current, want),
		Kubectl: kubectlScale(req.Target, want),
	}
	result.Output("replicasBefore", strconv.Itoa(int(current)))
	result.Output("replicasAfter", strconv.Itoa(int(want)))
	return result, nil
}

// Verify waits for the new replicas to be available.
//
// Requested is not the same as running: replicas that cannot schedule are
// not capacity, and a remediation that added them has not helped.
func (a *DeploymentScale) Verify(
	ctx context.Context, req action.Request, executed action.Result,
) (action.Result, error) {
	ctx, cancel := action.WithVerifyDeadline(ctx)
	defer cancel()

	want, err := strconv.Atoi(executed.Outputs["replicasAfter"])
	if err != nil {
		return action.Result{}, fmt.Errorf("no replica count was recorded to verify")
	}

	var last string
	for {
		var deployment appsv1.Deployment
		key := client.ObjectKey{Namespace: req.Target.Namespace, Name: req.Target.Name}
		if err := a.client.Get(ctx, key, &deployment); err != nil {
			return action.Result{Summary: last}, fmt.Errorf("read %s: %w", req.Target, err)
		}

		available := deployment.Status.AvailableReplicas
		last = fmt.Sprintf("%d/%d replicas available", available, want)
		if int(available) >= want {
			result := action.Result{Summary: last}
			result.Output("availableReplicas", strconv.Itoa(int(available)))
			return result, nil
		}

		select {
		case <-ctx.Done():
			return action.Result{Summary: last},
				fmt.Errorf("the new replicas did not become available in time: %s", last)
		case <-time.After(a.poll):
		}
	}
}

func (a *DeploymentScale) plan(ctx context.Context, req action.Request) (*appsv1.Deployment, int32, error) {
	var deployment appsv1.Deployment
	key := client.ObjectKey{Namespace: req.Target.Namespace, Name: req.Target.Name}
	if err := a.client.Get(ctx, key, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, 0, fmt.Errorf("%s does not exist: %w", req.Target, err)
		}
		return nil, 0, fmt.Errorf("read %s: %w", req.Target, err)
	}

	if err := a.checkAutoscaler(ctx, req); err != nil {
		return nil, 0, err
	}

	want, err := targetCount(req.Params, readRollout(&deployment).desired)
	if err != nil {
		return nil, 0, err
	}
	return &deployment, want, nil
}

// checkAutoscaler refuses a Deployment a HorizontalPodAutoscaler owns.
func (a *DeploymentScale) checkAutoscaler(ctx context.Context, req action.Request) error {
	var autoscalers autoscalingv2.HorizontalPodAutoscalerList
	if err := a.client.List(ctx, &autoscalers, client.InNamespace(req.Target.Namespace)); err != nil {
		if apierrors.IsForbidden(err) {
			// The check is a safety property, so failing to perform it is a
			// refusal rather than a shrug.
			return fmt.Errorf(
				"cannot tell whether %s is autoscaled, so remedik will not scale it: %w",
				req.Target, err)
		}
		return fmt.Errorf("list autoscalers in %s: %w", req.Target.Namespace, err)
	}

	for i := range autoscalers.Items {
		ref := autoscalers.Items[i].Spec.ScaleTargetRef
		if ref.Kind == "Deployment" && ref.Name == req.Target.Name {
			return fmt.Errorf(
				"%s is scaled by horizontalpodautoscaler/%s: setting replicas would be reverted "+
					"on its next interval, and remedik would have recorded a success that did not "+
					"stick. Use hpa.scale to raise its ceiling instead",
				req.Target, autoscalers.Items[i].Name)
		}
	}
	return nil
}

// HPAScale raises an autoscaler's ceiling.
//
// The one useful mechanical answer to KubeHpaMaxedOut: the autoscaler is
// pinned at its maximum and still under pressure, so the maximum is the
// thing that is wrong.
type HPAScale struct {
	client client.Client
}

// NewHPAScale builds the action.
func NewHPAScale(c client.Client) *HPAScale { return &HPAScale{client: c} }

// Name implements action.Action.
func (a *HPAScale) Name() string { return "hpa.scale" }

// LabelHPA is the alert label naming the autoscaler.
const LabelHPA = "horizontalpodautoscaler"

// Resolve determines the HorizontalPodAutoscaler.
func (a *HPAScale) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	namespace := params.Get(LabelNamespace, labels[LabelNamespace])
	name := params.Get(NameParam, params.Get(LabelHPA, labels[LabelHPA]))

	if namespace == "" {
		return action.Target{}, fmt.Errorf(
			"no namespace: the alert has no %q label and the step sets no %q parameter",
			LabelNamespace, LabelNamespace)
	}
	if name == "" {
		return action.Target{}, fmt.Errorf(
			"no autoscaler: the alert has no %q label and the step sets no %q parameter",
			LabelHPA, NameParam)
	}
	return action.Target{Kind: "HorizontalPodAutoscaler", Namespace: namespace, Name: name}, nil
}

// Plan reports the change.
func (a *HPAScale) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	hpa, want, err := a.plan(ctx, req)
	if err != nil {
		return action.Result{}, err
	}

	result := action.Result{
		Summary: fmt.Sprintf("raise %s maxReplicas from %d to %d",
			req.Target, hpa.Spec.MaxReplicas, want),
		Kubectl: kubectlPatchHPA(req.Target, want),
	}
	result.Output("maxReplicasBefore", strconv.Itoa(int(hpa.Spec.MaxReplicas)))
	result.Output("maxReplicasAfter", strconv.Itoa(int(want)))
	result.Output("currentReplicas", strconv.Itoa(int(hpa.Status.CurrentReplicas)))
	return result, nil
}

// Execute raises the ceiling.
func (a *HPAScale) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	hpa, want, err := a.plan(ctx, req)
	if err != nil {
		return action.Result{}, err
	}
	before := hpa.Spec.MaxReplicas

	patch := []byte(fmt.Sprintf(`{"spec":{"maxReplicas":%d}}`, want))
	if err := a.client.Patch(ctx, hpa, client.RawPatch(mergePatch, patch)); err != nil {
		if apierrors.IsForbidden(err) {
			return action.Result{}, fmt.Errorf("not permitted to patch %s: %w", req.Target, err)
		}
		return action.Result{}, fmt.Errorf("patch %s: %w", req.Target, err)
	}

	result := action.Result{
		Summary: fmt.Sprintf("raised %s maxReplicas from %d to %d; the autoscaler decides "+
			"whether to use the headroom", req.Target, before, want),
		Kubectl: kubectlPatchHPA(req.Target, want),
	}
	result.Output("maxReplicasBefore", strconv.Itoa(int(before)))
	result.Output("maxReplicasAfter", strconv.Itoa(int(want)))
	return result, nil
}

func (a *HPAScale) plan(
	ctx context.Context, req action.Request,
) (*autoscalingv2.HorizontalPodAutoscaler, int32, error) {
	var hpa autoscalingv2.HorizontalPodAutoscaler
	key := client.ObjectKey{Namespace: req.Target.Namespace, Name: req.Target.Name}
	if err := a.client.Get(ctx, key, &hpa); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, 0, fmt.Errorf("%s does not exist: %w", req.Target, err)
		}
		return nil, 0, fmt.Errorf("read %s: %w", req.Target, err)
	}

	want, err := targetCount(req.Params, hpa.Spec.MaxReplicas)
	if err != nil {
		return nil, 0, err
	}
	if want <= hpa.Spec.MaxReplicas {
		return nil, 0, fmt.Errorf(
			"%s already allows %d replicas, and this step asks for %d: lowering an autoscaler's "+
				"ceiling during an incident is not a remediation",
			req.Target, hpa.Spec.MaxReplicas, want)
	}
	return &hpa, want, nil
}

// targetCount works out the number a scaling step is asking for.
//
// A relative change must state a maximum. "Increase by" with no ceiling is
// an alert storm with a credit card, and a default ceiling would be a number
// this code invented for somebody else's cluster and budget.
func targetCount(params action.Params, current int32) (int32, error) {
	absolute := params.Get(ReplicasParam, "")
	by := params.Get(IncreaseByParam, "")
	percent := params.Get(IncreasePercentParam, "")

	set := 0
	for _, v := range []string{absolute, by, percent} {
		if v != "" {
			set++
		}
	}
	switch set {
	case 0:
		return 0, fmt.Errorf("the step must set one of %q, %q or %q",
			ReplicasParam, IncreaseByParam, IncreasePercentParam)
	case 1:
	default:
		return 0, fmt.Errorf("the step sets more than one of %q, %q and %q; they mean different things",
			ReplicasParam, IncreaseByParam, IncreasePercentParam)
	}

	if absolute != "" {
		want, err := parsePositiveInt32(absolute, ReplicasParam)
		if err != nil {
			return 0, err
		}
		return want, nil
	}

	ceiling, err := parsePositiveInt32(params.Get(MaxParam, ""), MaxParam)
	if err != nil {
		return 0, fmt.Errorf("a relative change needs a ceiling: %w", err)
	}

	var want int32
	switch {
	case by != "":
		delta, err := parsePositiveInt32(by, IncreaseByParam)
		if err != nil {
			return 0, err
		}
		want = current + delta
	default:
		share, err := parsePositiveInt32(percent, IncreasePercentParam)
		if err != nil {
			return 0, err
		}
		// Rounded up, so increasing a 3-replica workload by 10% adds one
		// rather than nothing.
		want = current + (current*share+99)/100
	}

	if want > ceiling {
		want = ceiling
	}
	if want <= current {
		return 0, fmt.Errorf("the ceiling of %d is already reached at %d replicas: "+
			"raise %q if there is more headroom to give", ceiling, current, MaxParam)
	}
	return want, nil
}

func parsePositiveInt32(raw, param string) (int32, error) {
	if raw == "" {
		return 0, fmt.Errorf("the step sets no %q", param)
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parameter %q: %q is not a whole number", param, raw)
	}
	if n <= 0 {
		return 0, fmt.Errorf("parameter %q: %d is not a positive number", param, n)
	}
	return int32(n), nil
}

func kubectlScale(target action.Target, replicas int32) string {
	return fmt.Sprintf("kubectl scale deployment/%s -n %s --replicas=%d",
		target.Name, target.Namespace, replicas)
}

func kubectlPatchHPA(target action.Target, ceiling int32) string {
	return fmt.Sprintf(
		`kubectl patch hpa %s -n %s --type=merge -p '{"spec":{"maxReplicas":%d}}'`,
		target.Name, target.Namespace, ceiling)
}

func joinComma(values []string) string { return strings.Join(values, ", ") }

// Compile-time proof that the actions satisfy the contract.
var (
	_ action.Action   = (*DeploymentScale)(nil)
	_ action.Verifier = (*DeploymentScale)(nil)
	_ action.Action   = (*HPAScale)(nil)
)
