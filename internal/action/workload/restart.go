// Package workload implements remediation actions that operate on
// Kubernetes workloads.
package workload

import (
	"context"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

// RestartAnnotation is the annotation patched onto a Deployment's pod
// template to trigger a rolling restart. It is the same key `kubectl
// rollout restart` uses, so a remediation looks like what an operator would
// have done by hand, and the two do not fight each other.
const RestartAnnotation = "kubectl.kubernetes.io/restartedAt"

// LabelNamespace and LabelDeployment are the alert labels a target is
// resolved from when the step does not name one explicitly.
const (
	LabelNamespace  = "namespace"
	LabelDeployment = "deployment"
)

// DeploymentRestart performs a rolling restart of a Deployment.
//
// It never deletes pods: patching the pod template hands the rollout to the
// Deployment controller, which respects maxUnavailable, readiness probes
// and PodDisruptionBudgets. Deleting pods would bypass all three.
type DeploymentRestart struct {
	client client.Client
	now    func() time.Time
	poll   time.Duration
}

// DefaultVerifyPoll is how often Verify re-reads the Deployment while it
// waits. Rollouts move in seconds, and a tighter loop would only spend the
// API server's budget to learn the same thing.
const DefaultVerifyPoll = 2 * time.Second

// NewDeploymentRestart builds the action. A nil clock uses time.Now.
func NewDeploymentRestart(c client.Client, now func() time.Time) *DeploymentRestart {
	if now == nil {
		now = time.Now
	}
	return &DeploymentRestart{client: c, now: now, poll: DefaultVerifyPoll}
}

// Name implements action.Action.
func (a *DeploymentRestart) Name() string { return "deployment.restart" }

// Resolve determines the Deployment from the alert's labels, or from the
// step's `namespace` and `deployment` parameters when given.
//
// Alerts about a pod usually carry the pod name rather than its owner. This
// action does not guess the Deployment from a pod name — deriving an owner
// from a name pattern is exactly the kind of guess that restarts the wrong
// workload — so the alert must carry a `deployment` label, or the strategy
// must name one.
func (a *DeploymentRestart) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	namespace := params.Get(LabelNamespace, labels[LabelNamespace])
	name := params.Get(LabelDeployment, labels[LabelDeployment])

	if namespace == "" {
		return action.Target{}, fmt.Errorf(
			"no namespace: the alert has no %q label and the step sets no %q parameter",
			LabelNamespace, LabelNamespace)
	}
	if name == "" {
		return action.Target{}, fmt.Errorf(
			"no deployment: the alert has no %q label and the step sets no %q parameter",
			LabelDeployment, LabelDeployment)
	}

	return action.Target{Kind: "Deployment", Namespace: namespace, Name: name}, nil
}

// Plan reports what Execute would do, and verifies the Deployment exists so
// that dry-run surfaces a missing target instead of reporting success for
// something that could never work.
func (a *DeploymentRestart) Plan(ctx context.Context, target action.Target, _ action.Params) (action.Result, error) {
	deployment, err := a.fetch(ctx, target)
	if err != nil {
		return action.Result{}, err
	}

	result := action.Result{
		Summary: fmt.Sprintf("restart %s (%d replicas) by patching %s on its pod template",
			target, replicas(deployment), RestartAnnotation),
		Kubectl: kubectlRestart(target),
	}
	result.Output("replicas", strconv.Itoa(int(replicas(deployment))))
	result.Output("readyReplicas", strconv.Itoa(int(deployment.Status.ReadyReplicas)))
	return result, nil
}

// Execute triggers the rolling restart.
func (a *DeploymentRestart) Execute(ctx context.Context, target action.Target, _ action.Params) (action.Result, error) {
	deployment, err := a.fetch(ctx, target)
	if err != nil {
		return action.Result{}, err
	}

	stamp := a.now().UTC().Format(time.RFC3339)
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{%q:%q}}}}}`,
		RestartAnnotation, stamp))

	if err := a.client.Patch(ctx, deployment, client.RawPatch(types.StrategicMergePatchType, patch)); err != nil {
		if apierrors.IsForbidden(err) {
			return action.Result{}, fmt.Errorf("not permitted to patch %s: %w", target, err)
		}
		return action.Result{}, fmt.Errorf("patch %s: %w", target, err)
	}

	result := action.Result{
		Summary: fmt.Sprintf("restarted %s: set %s=%s (resourceVersion %s)",
			target, RestartAnnotation, stamp, deployment.GetResourceVersion()),
		Kubectl: kubectlRestart(target),
	}
	result.Output("restartedAt", stamp)
	result.Output("replicas", strconv.Itoa(int(replicas(deployment))))
	result.Output("resourceVersion", deployment.GetResourceVersion())
	return result, nil
}

// Verify waits for the rollout to finish.
//
// Patching the annotation only means the API server accepted it. The
// question an operator is actually asking — did the workload come back? —
// is answered by the Deployment's status, and only after the controller has
// observed the new generation. Reporting success on the patch alone is
// reporting on the wrong event.
func (a *DeploymentRestart) Verify(
	ctx context.Context, target action.Target, _ action.Params,
) (action.Result, error) {
	var last string

	for {
		deployment, err := a.fetch(ctx, target)
		if err != nil {
			return action.Result{Summary: last}, err
		}

		state, done := rolloutState(deployment)
		last = state
		if done {
			result := action.Result{Summary: state}
			result.Output("readyReplicas", strconv.Itoa(int(deployment.Status.ReadyReplicas)))
			result.Output("updatedReplicas", strconv.Itoa(int(deployment.Status.UpdatedReplicas)))
			return result, nil
		}

		select {
		case <-ctx.Done():
			// The deadline is the step's verifyTimeout. Saying what the
			// rollout had reached when time ran out is more useful than
			// saying that time ran out.
			return action.Result{Summary: state},
				fmt.Errorf("the rollout did not complete in time: %s", state)
		case <-time.After(a.poll):
		}
	}
}

// rolloutState describes how far a rollout has got, and whether it is done.
//
// The generation check comes first and matters most: without it, a status
// left over from before the patch reads as a completed rollout, and the
// verification passes before anything has happened at all.
func rolloutState(d *appsv1.Deployment) (string, bool) {
	want := replicas(d)

	if d.Status.ObservedGeneration < d.Generation {
		return fmt.Sprintf("waiting for the controller to observe generation %d (at %d)",
			d.Generation, d.Status.ObservedGeneration), false
	}

	switch {
	case d.Status.UpdatedReplicas < want:
		return fmt.Sprintf("%d/%d replicas updated", d.Status.UpdatedReplicas, want), false
	case d.Status.Replicas > d.Status.UpdatedReplicas:
		return fmt.Sprintf("%d old replicas still terminating",
			d.Status.Replicas-d.Status.UpdatedReplicas), false
	case d.Status.AvailableReplicas < want:
		return fmt.Sprintf("%d/%d replicas available", d.Status.AvailableReplicas, want), false
	default:
		return fmt.Sprintf("%d/%d replicas updated, available and ready",
			d.Status.ReadyReplicas, want), true
	}
}

// kubectlRestart is the command a human would have typed. It is recorded,
// never executed.
func kubectlRestart(target action.Target) string {
	return fmt.Sprintf("kubectl rollout restart deployment/%s -n %s", target.Name, target.Namespace)
}

func (a *DeploymentRestart) fetch(ctx context.Context, target action.Target) (*appsv1.Deployment, error) {
	var deployment appsv1.Deployment
	key := client.ObjectKey{Namespace: target.Namespace, Name: target.Name}

	if err := a.client.Get(ctx, key, &deployment); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return nil, fmt.Errorf("%s does not exist: %w", target, err)
		case apierrors.IsForbidden(err):
			return nil, fmt.Errorf("not permitted to read %s: %w", target, err)
		default:
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
	}
	return &deployment, nil
}

func replicas(d *appsv1.Deployment) int32 {
	if d.Spec.Replicas == nil {
		return 1
	}
	return *d.Spec.Replicas
}

// Compile-time proof that the action satisfies the contract.
var (
	_ action.Action   = (*DeploymentRestart)(nil)
	_ action.Verifier = (*DeploymentRestart)(nil)
)
