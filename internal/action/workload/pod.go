package workload

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

// LabelPod is the alert label naming the pod to evict.
const LabelPod = "pod"

// RequireOwnerParam controls the refusal to remove a pod nothing would
// recreate.
const RequireOwnerParam = "requireOwner"

// GracePeriodParam overrides the pod's own termination grace period.
const GracePeriodParam = "gracePeriodSeconds"

// PodDelete removes one pod so that its controller replaces it.
//
// It evicts rather than deletes, and the distinction is the whole point.
// Deleting a pod ignores PodDisruptionBudgets entirely; the Eviction API is
// the only call that checks them, answering 429 when the removal would
// breach the budget. During an incident that is exactly the moment a tool
// must not take down the last healthy replica of something — so a refusal
// is recorded as a refusal, and the pod stays up.
type PodDelete struct {
	client client.Client
	poll   time.Duration
}

// NewPodDelete builds the action.
func NewPodDelete(c client.Client) *PodDelete {
	return &PodDelete{client: c, poll: DefaultVerifyPoll}
}

// Name implements action.Action.
func (a *PodDelete) Name() string { return "pod.delete" }

// Resolve determines the pod from the alert's `namespace` and `pod` labels,
// or from the step's parameters.
func (a *PodDelete) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	namespace := params.Get(LabelNamespace, labels[LabelNamespace])
	name := params.Get(LabelPod, labels[LabelPod])

	if namespace == "" {
		return action.Target{}, fmt.Errorf(
			"no namespace: the alert has no %q label and the step sets no %q parameter",
			LabelNamespace, LabelNamespace)
	}
	if name == "" {
		return action.Target{}, fmt.Errorf(
			"no pod: the alert has no %q label and the step sets no %q parameter",
			LabelPod, LabelPod)
	}

	return action.Target{Kind: "Pod", Namespace: namespace, Name: name}, nil
}

// Plan reports what Execute would do, and performs every check Execute
// performs, so that a dry run surfaces a pod that could not be evicted
// rather than promising one that could not work.
func (a *PodDelete) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	target, params := req.Target, req.Params
	pod, err := a.fetch(ctx, target)
	if err != nil {
		return action.Result{}, err
	}
	if err := checkOwner(pod, target, params); err != nil {
		return action.Result{}, err
	}

	owner := ownerDescription(pod)
	result := action.Result{
		Summary: fmt.Sprintf("evict %s through the Eviction API, so %s replaces it "+
			"(a PodDisruptionBudget may refuse)", target, owner),
		Kubectl: kubectlEvict(target),
	}
	result.Output("owner", owner)
	result.Output("node", pod.Spec.NodeName)
	result.Output("phase", string(pod.Status.Phase))
	result.Output("uid", string(pod.UID))
	return result, nil
}

// Execute evicts the pod.
func (a *PodDelete) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	target, params := req.Target, req.Params
	pod, err := a.fetch(ctx, target)
	if err != nil {
		return action.Result{}, err
	}
	if err := checkOwner(pod, target, params); err != nil {
		return action.Result{}, err
	}

	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{Namespace: target.Namespace, Name: target.Name},
	}
	grace, err := gracePeriod(params)
	if err != nil {
		return action.Result{}, err
	}
	if grace != nil {
		eviction.DeleteOptions = &metav1.DeleteOptions{GracePeriodSeconds: grace}
	}

	if err := a.client.SubResource("eviction").Create(ctx, pod, eviction); err != nil {
		switch {
		case apierrors.IsTooManyRequests(err):
			// The one failure worth explaining carefully: this is the API
			// telling us the removal would breach a disruption budget.
			return action.Result{}, fmt.Errorf(
				"a PodDisruptionBudget refused the eviction of %s: %w. "+
					"The pod is still running; the retry budget will try again after the backoff",
				target, err)
		case apierrors.IsForbidden(err):
			return action.Result{}, fmt.Errorf("not permitted to evict %s: %w", target, err)
		case apierrors.IsNotFound(err):
			return action.Result{}, fmt.Errorf("%s went away before it could be evicted: %w", target, err)
		default:
			return action.Result{}, fmt.Errorf("evict %s: %w", target, err)
		}
	}

	result := action.Result{
		Summary: fmt.Sprintf("evicted %s from node %s; %s will replace it",
			target, nodeOrUnknown(pod), ownerDescription(pod)),
		Kubectl: kubectlEvict(target),
	}
	result.Output("owner", ownerDescription(pod))
	result.Output("node", pod.Spec.NodeName)
	result.Output("uid", string(pod.UID))
	return result, nil
}

// Verify waits for the pod to actually go.
//
// An eviction request that returned 201 has been accepted, not completed:
// the pod runs until its grace period expires, and a finalizer can hold it
// longer than that. Gone, or replaced by a pod of the same name with a
// different UID, is the outcome worth reporting — for a StatefulSet the
// replacement keeps the name, and the UID Execute recorded is the only
// thing that tells it apart from the one still terminating.
func (a *PodDelete) Verify(
	ctx context.Context, req action.Request, executed action.Result,
) (action.Result, error) {
	target := req.Target
	ctx, cancel := action.WithVerifyDeadline(ctx)
	defer cancel()

	was := executed.Outputs["uid"]

	for {
		pod, err := a.fetch(ctx, target)
		switch {
		case apierrors.IsNotFound(err):
			result := action.Result{Summary: fmt.Sprintf("%s is gone", target)}
			result.Output("outcome", "deleted")
			return result, nil
		case err != nil:
			return action.Result{}, err
		case was != "" && string(pod.UID) != was:
			result := action.Result{Summary: fmt.Sprintf("%s was replaced (new UID %s)", target, pod.UID)}
			result.Output("outcome", "replaced")
			return result, nil
		}

		says := fmt.Sprintf("%s is still %s", target, strings.ToLower(string(pod.Status.Phase)))
		if pod.DeletionTimestamp != nil {
			says = fmt.Sprintf("%s is terminating", target)
		}

		select {
		case <-ctx.Done():
			return action.Result{Summary: says},
				fmt.Errorf("the pod did not go away in time: %s", says)
		case <-time.After(a.poll):
		}
	}
}

func (a *PodDelete) fetch(ctx context.Context, target action.Target) (*corev1.Pod, error) {
	var pod corev1.Pod
	key := client.ObjectKey{Namespace: target.Namespace, Name: target.Name}

	if err := a.client.Get(ctx, key, &pod); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return nil, fmt.Errorf("%s does not exist: %w", target, err)
		case apierrors.IsForbidden(err):
			return nil, fmt.Errorf("not permitted to read %s: %w", target, err)
		default:
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
	}
	return &pod, nil
}

// checkOwner refuses a pod nothing would recreate.
//
// Deleting a bare pod is not remediation, it is deletion: the workload does
// not come back, and the alert that fired is replaced by a quieter problem.
// The refusal is a default rather than a rule because someone will have a
// reason, and a strategy is where they should write it down.
func checkOwner(pod *corev1.Pod, target action.Target, params action.Params) error {
	if params.Get(RequireOwnerParam, "true") == "false" {
		return nil
	}
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return nil
		}
	}
	return fmt.Errorf(
		"%s has no controller owner, so nothing would recreate it: evicting it would be a deletion, "+
			"not a remediation. Set %s=\"false\" on the step if that is genuinely what you want",
		target, RequireOwnerParam)
}

func ownerDescription(pod *corev1.Pod) string {
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return fmt.Sprintf("%s/%s", lower(ref.Kind), ref.Name)
		}
	}
	return "nothing"
}

func nodeOrUnknown(pod *corev1.Pod) string {
	if pod.Spec.NodeName == "" {
		return "(unscheduled)"
	}
	return pod.Spec.NodeName
}

// gracePeriod reads an override for the pod's termination grace period.
func gracePeriod(params action.Params) (*int64, error) {
	raw := params.Get(GracePeriodParam, "")
	if raw == "" {
		return nil, nil
	}
	seconds, err := parseNonNegativeInt(raw)
	if err != nil {
		return nil, fmt.Errorf("parameter %q: %w", GracePeriodParam, err)
	}
	return &seconds, nil
}

func kubectlEvict(target action.Target) string {
	// There is no kubectl verb for a single eviction; `kubectl delete pod`
	// is what a person would type, and the note is there because it is not
	// quite the same call.
	return fmt.Sprintf("kubectl delete pod %s -n %s  # remedik uses the Eviction API, which honours PodDisruptionBudgets",
		target.Name, target.Namespace)
}

// Compile-time proof that the action satisfies the contract.
var (
	_ action.Action   = (*PodDelete)(nil)
	_ action.Verifier = (*PodDelete)(nil)
)
