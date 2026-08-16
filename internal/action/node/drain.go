package node

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/internal/action"
)

// Parameters of node.drain.
const (
	// DeleteEmptyDirParam allows evicting pods with emptyDir volumes, whose
	// contents are lost when they move.
	DeleteEmptyDirParam = "deleteEmptyDirData"
	// EvictBarePodsParam allows evicting pods with no controller. Nothing
	// recreates one, so this is deletion rather than remediation.
	EvictBarePodsParam = "evictBarePods"
	// MaxPodsParam refuses a node holding more than this many evictable
	// pods, for a strategy that will drain a small node unattended but not
	// a large one.
	MaxPodsParam = "maxPods"
)

// mirrorPodAnnotation marks a pod the kubelet manages from a file on disk.
// It cannot be evicted at all: the API server has no authority over it.
const mirrorPodAnnotation = "kubernetes.io/config.mirror"

// retryAfterRefusal is how long to wait before asking again when a
// PodDisruptionBudget refuses. During a drain a 429 means "not yet", not
// "no": kubectl drain retries on the same rhythm.
const retryAfterRefusal = 5 * time.Second

// Drain empties a node, honouring the disruption budgets its workloads
// declared.
//
// It is the widest action remedik has, and the one most worth leaving in
// dry-run for a long time. Two properties matter more than the rest:
//
//   - It evicts, so a PodDisruptionBudget can refuse. Unlike everywhere else
//     in remedik a refusal is not immediately fatal here — a drain is a loop
//     by nature, and "not yet" is the normal answer partway through one.
//   - A drain that does not finish is a failure. Half-drained is the worst
//     state to leave a node in: cordoned, missing some of its work, and
//     nobody knowing whether to continue or reverse. Reporting it as done
//     would lose capacity that no dashboard accounts for.
type Drain struct {
	client client.Client
	poll   time.Duration
}

// NewDrain builds the action.
func NewDrain(c client.Client) *Drain {
	return &Drain{client: c, poll: retryAfterRefusal}
}

// Name implements action.Action.
func (a *Drain) Name() string { return "node.drain" }

// Resolve determines the node.
func (a *Drain) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	return resolveNode(labels, params)
}

// Plan reports what would be evicted, without evicting anything.
//
// This is the most useful dry-run report in the catalogue: it names every
// pod that would move, which is the list somebody wants before allowing a
// drain to happen unattended.
func (a *Drain) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	node, err := fetchNode(ctx, a.client, req.Target)
	if err != nil {
		return action.Result{}, err
	}

	evictable, skipped, err := a.classify(ctx, req)
	if err != nil {
		return action.Result{}, err
	}

	result := action.Result{
		Summary: fmt.Sprintf(
			"cordon %s and evict %s through the Eviction API, honouring PodDisruptionBudgets (%s)",
			req.Target, plural(len(evictable), "pod"), summarise(skipped)),
		Kubectl: kubectlDrain(req.Target),
	}
	result.Output("podsToEvict", strconv.Itoa(len(evictable)))
	result.Output("pods", names(evictable))
	result.Output("skipped", strconv.Itoa(len(skipped)))
	result.Output("alreadyCordoned", strconv.FormatBool(node.Spec.Unschedulable))
	return result, nil
}

// Execute cordons the node and evicts its pods.
func (a *Drain) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	node, err := fetchNode(ctx, a.client, req.Target)
	if err != nil {
		return action.Result{}, err
	}

	evictable, skipped, err := a.classify(ctx, req)
	if err != nil {
		return action.Result{}, err
	}

	// Cordon first. Draining without it is a race against the scheduler:
	// pods evicted from the node can be placed straight back onto it.
	if !node.Spec.Unschedulable {
		patch := []byte(`{"spec":{"unschedulable":true}}`)
		if err := a.client.Patch(ctx, node, client.RawPatch(mergePatch, patch)); err != nil {
			return action.Result{}, fmt.Errorf("cordon %s before draining: %w", req.Target, err)
		}
	}

	evicted, refused, err := a.evictAll(ctx, req.Target, evictable)

	result := action.Result{
		Summary: fmt.Sprintf("cordoned %s and evicted %s (%s)",
			req.Target, plural(evicted, "pod"), summarise(skipped)),
		Kubectl: kubectlDrain(req.Target),
	}
	result.Output("evicted", strconv.Itoa(evicted))
	result.Output("skipped", strconv.Itoa(len(skipped)))
	if len(refused) > 0 {
		result.Output("remaining", names(refused))
	}

	if err != nil {
		// The node stays cordoned. Uncordoning one somebody is mid-way
		// through draining would be worse than leaving it, and the record
		// says exactly what is left.
		result.Summary = fmt.Sprintf("drained %s of %s and stopped: %s still there",
			req.Target, plural(evicted, "pod"), plural(len(refused), "pod"))
		return result, err
	}
	return result, nil
}

// Verify confirms the node is empty of what the drain was meant to remove.
func (a *Drain) Verify(
	ctx context.Context, req action.Request, _ action.Result,
) (action.Result, error) {
	ctx, cancel := action.WithVerifyDeadline(ctx)
	defer cancel()

	for {
		evictable, _, err := a.classify(ctx, req)
		if err != nil {
			return action.Result{}, err
		}
		if len(evictable) == 0 {
			result := action.Result{Summary: fmt.Sprintf("%s is drained", req.Target)}
			result.Output("remainingPods", "0")
			return result, nil
		}

		says := fmt.Sprintf("%s still holds %s: %s",
			req.Target, plural(len(evictable), "pod"), names(evictable))

		select {
		case <-ctx.Done():
			result := action.Result{Summary: says}
			result.Output("remainingPods", strconv.Itoa(len(evictable)))
			// Half-drained is the worst state to leave a node in, so it is
			// reported as the failure it is rather than as partial success.
			return result, fmt.Errorf("the drain did not finish in time: %s", says)
		case <-time.After(a.poll):
		}
	}
}

// evictAll evicts every pod, retrying the ones a disruption budget refuses.
func (a *Drain) evictAll(
	ctx context.Context, target action.Target, pods []corev1.Pod,
) (evicted int, refused []corev1.Pod, err error) {
	remaining := pods

	for {
		var stillRefused []corev1.Pod

		for i := range remaining {
			pod := &remaining[i]
			evictErr := a.evict(ctx, pod)
			switch {
			case evictErr == nil, apierrors.IsNotFound(evictErr):
				// Gone, either because this call removed it or because it
				// had already left.
				evicted++
			case apierrors.IsTooManyRequests(evictErr):
				// A disruption budget saying "not yet". During a drain that
				// is the normal answer partway through, not a failure.
				stillRefused = append(stillRefused, *pod)
			case apierrors.IsForbidden(evictErr):
				return evicted, append(stillRefused, remaining[i:]...),
					fmt.Errorf("not permitted to evict %s/%s: %w",
						pod.Namespace, pod.Name, evictErr)
			default:
				return evicted, append(stillRefused, remaining[i:]...),
					fmt.Errorf("evict %s/%s: %w", pod.Namespace, pod.Name, evictErr)
			}
		}

		if len(stillRefused) == 0 {
			return evicted, nil, nil
		}
		remaining = stillRefused

		select {
		case <-ctx.Done():
			return evicted, remaining, fmt.Errorf(
				"%s is only partly drained: %s still there, refused by a PodDisruptionBudget. "+
					"The node stays cordoned; nothing new will be scheduled on it",
				target, plural(len(remaining), "pod"))
		case <-time.After(a.poll):
		}
	}
}

func (a *Drain) evict(ctx context.Context, pod *corev1.Pod) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{Namespace: pod.Namespace, Name: pod.Name},
	}
	return a.client.SubResource("eviction").Create(ctx, pod, eviction)
}

// skipped records a pod the drain deliberately left alone, and why.
type skippedPod struct {
	pod    string
	reason string
}

// classify splits the node's pods into what will be evicted and what will
// not.
func (a *Drain) classify(
	ctx context.Context, req action.Request,
) (evictable []corev1.Pod, skipped []skippedPod, err error) {
	var pods corev1.PodList
	if err := a.client.List(ctx, &pods,
		client.MatchingFields{"spec.nodeName": req.Target.Name}); err != nil {
		return nil, nil, fmt.Errorf("list the pods on %s: %w", req.Target, err)
	}

	allowEmptyDir := req.Params.Get(DeleteEmptyDirParam, "false") == "true"
	allowBare := req.Params.Get(EvictBarePodsParam, "false") == "true"

	for i := range pods.Items {
		pod := pods.Items[i]

		switch {
		case pod.Spec.NodeName != req.Target.Name:
			// A field selector the API server did not honour; check anyway
			// rather than evict something from another node.
			continue

		case isTerminal(&pod):
			skipped = append(skipped, skippedPod{pod: podName(&pod), reason: "already finished"})

		case pod.Annotations[mirrorPodAnnotation] != "":
			// A static pod: the kubelet owns it, and the API server has no
			// authority to evict it.
			skipped = append(skipped, skippedPod{pod: podName(&pod), reason: "a mirror pod"})

		case ownerKind(&pod) == "DaemonSet":
			// Its controller puts it straight back, so evicting it is a
			// loop that never ends. kubectl drain needs a flag for this;
			// here it is the default, because a remediation that needs a
			// flag to terminate will be run without it.
			skipped = append(skipped, skippedPod{pod: podName(&pod), reason: "owned by a DaemonSet"})

		case ownerKind(&pod) == "" && !allowBare:
			// Nothing recreates a bare pod, so evicting it is deletion.
			skipped = append(skipped, skippedPod{
				pod:    podName(&pod),
				reason: "has no controller, so nothing would recreate it",
			})

		case hasEmptyDir(&pod) && !allowEmptyDir:
			skipped = append(skipped, skippedPod{
				pod:    podName(&pod),
				reason: "uses an emptyDir whose contents would be lost",
			})

		default:
			evictable = append(evictable, pod)
		}
	}

	sort.Slice(evictable, func(i, j int) bool { return podName(&evictable[i]) < podName(&evictable[j]) })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].pod < skipped[j].pod })

	if limit := req.Params.Get(MaxPodsParam, ""); limit != "" {
		n, convErr := strconv.Atoi(limit)
		if convErr != nil || n <= 0 {
			return nil, nil, fmt.Errorf("parameter %q: %q is not a positive number", MaxPodsParam, limit)
		}
		if len(evictable) > n {
			return nil, nil, fmt.Errorf(
				"%s holds %d evictable pods and the step allows %d: draining it would move more "+
					"than this strategy said it may",
				req.Target, len(evictable), n)
		}
	}

	return evictable, skipped, nil
}

func isTerminal(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

func ownerKind(pod *corev1.Pod) string {
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return ref.Kind
		}
	}
	return ""
}

func hasEmptyDir(pod *corev1.Pod) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.EmptyDir != nil {
			return true
		}
	}
	return false
}

func podName(pod *corev1.Pod) string { return pod.Namespace + "/" + pod.Name }

func names(pods []corev1.Pod) string {
	const shown = 10
	out := make([]string, 0, len(pods))
	for i := range pods {
		if i == shown {
			out = append(out, fmt.Sprintf("and %d more", len(pods)-shown))
			break
		}
		out = append(out, podName(&pods[i]))
	}
	return strings.Join(out, ", ")
}

// summarise says what was left alone and why, grouped so the sentence stays
// readable on a node with fifty DaemonSet pods.
func summarise(skipped []skippedPod) string {
	if len(skipped) == 0 {
		return "nothing skipped"
	}

	counts := map[string]int{}
	for _, s := range skipped {
		counts[s.reason]++
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)

	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, fmt.Sprintf("%d %s", counts[reason], reason))
	}
	return "skipping " + strings.Join(parts, ", ")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func kubectlDrain(target action.Target) string {
	return fmt.Sprintf(
		"kubectl drain %s --ignore-daemonsets --delete-emptydir-data=false", target.Name)
}

// Compile-time proof that the action satisfies the contract.
var (
	_ action.Action   = (*Drain)(nil)
	_ action.Verifier = (*Drain)(nil)
)
