package engine

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
	"github.com/ratyx/remedik/internal/guards"
)

// WorkloadHealth answers, for the blastRadius guard, how much of the
// workload behind a target is up.
//
// It reads through a direct client rather than the manager's cache: caching
// every Deployment in the cluster to look at a handful during incidents
// would cost memory permanently for an occasional read, and would force the
// chart to grant list and watch where get is enough. That is the same
// argument the actions already make.
type WorkloadHealth struct {
	// Reader reads workloads and pods. Nil means the guard cannot evaluate,
	// which it treats as a refusal rather than as permission.
	Reader client.Reader
}

// Workload implements guards.WorkloadReader.
//
// The boolean distinguishes "there is nothing here to measure" from "I could
// not measure it". A node has no replica count and an action that touches
// nothing has no workload; both are `applicable=false`, and the guard allows.
// An error is something else entirely, and the guard refuses.
func (w *WorkloadHealth) Workload(
	ctx context.Context, target string,
) (guards.Workload, bool, error) {
	if target == "" {
		return guards.Workload{}, false, nil
	}

	parsed, err := action.ParseTarget(target)
	if err != nil {
		// A target that cannot be parsed is a bug, not a workload. Saying
		// so is more useful than refusing every remediation on a strategy
		// whose action resolves oddly.
		return guards.Workload{}, false, nil
	}

	switch strings.ToLower(parsed.Kind) {
	case "deployment":
		return w.deployment(ctx, parsed)
	case "statefulset":
		return w.statefulSet(ctx, parsed)
	case "daemonset":
		return w.daemonSet(ctx, parsed)
	case "pod":
		return w.behindPod(ctx, parsed)
	default:
		// A node, a PVC, a Job: nothing with a replica count, so nothing
		// this guard can say anything about.
		return guards.Workload{}, false, nil
	}
}

func (w *WorkloadHealth) deployment(ctx context.Context, target action.Target) (guards.Workload, bool, error) {
	var d appsv1.Deployment
	if err := w.get(ctx, target, &d); err != nil {
		return guards.Workload{}, false, err
	}
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	return guards.Workload{
		Name: target.String(), Desired: desired, Available: d.Status.AvailableReplicas,
	}, true, nil
}

func (w *WorkloadHealth) statefulSet(ctx context.Context, target action.Target) (guards.Workload, bool, error) {
	var s appsv1.StatefulSet
	if err := w.get(ctx, target, &s); err != nil {
		return guards.Workload{}, false, err
	}
	desired := int32(1)
	if s.Spec.Replicas != nil {
		desired = *s.Spec.Replicas
	}
	return guards.Workload{
		Name: target.String(), Desired: desired, Available: s.Status.AvailableReplicas,
	}, true, nil
}

func (w *WorkloadHealth) daemonSet(ctx context.Context, target action.Target) (guards.Workload, bool, error) {
	var d appsv1.DaemonSet
	if err := w.get(ctx, target, &d); err != nil {
		return guards.Workload{}, false, err
	}
	// A DaemonSet counts nodes; the arithmetic is the same question with a
	// different denominator.
	return guards.Workload{
		Name:      target.String(),
		Desired:   d.Status.DesiredNumberScheduled,
		Available: d.Status.NumberAvailable,
	}, true, nil
}

// behindPod resolves a pod to the workload that owns it.
//
// pod.delete targets a pod, but the question the guard is asking is about
// the thing behind it. The walk is pod → ReplicaSet → Deployment, or pod
// straight to a StatefulSet or DaemonSet. A pod with no controller is
// already refused by pod.delete itself, so this never has to invent an
// answer for one.
func (w *WorkloadHealth) behindPod(ctx context.Context, target action.Target) (guards.Workload, bool, error) {
	var pod corev1.Pod
	if err := w.get(ctx, target, &pod); err != nil {
		return guards.Workload{}, false, err
	}

	owner := controllerOf(pod.OwnerReferences)
	if owner == nil {
		// Nothing owns it, so there is no workload to measure. pod.delete
		// refuses such a pod on its own account, for its own reasons.
		return guards.Workload{}, false, nil
	}

	switch owner.Kind {
	case "ReplicaSet":
		var rs appsv1.ReplicaSet
		key := client.ObjectKey{Namespace: target.Namespace, Name: owner.Name}
		if err := w.Reader.Get(ctx, key, &rs); err != nil {
			return guards.Workload{}, false, fmt.Errorf("read replicaset/%s/%s: %w",
				target.Namespace, owner.Name, err)
		}
		deployment := controllerOf(rs.OwnerReferences)
		if deployment == nil || deployment.Kind != "Deployment" {
			// A bare ReplicaSet is a workload in its own right.
			return guards.Workload{
				Name:      "replicaset/" + target.Namespace + "/" + rs.Name,
				Desired:   replicasOf(rs.Spec.Replicas),
				Available: rs.Status.AvailableReplicas,
			}, true, nil
		}
		return w.deployment(ctx, action.Target{
			Kind: "Deployment", Namespace: target.Namespace, Name: deployment.Name,
		})

	case "StatefulSet":
		return w.statefulSet(ctx, action.Target{
			Kind: "StatefulSet", Namespace: target.Namespace, Name: owner.Name,
		})

	case "DaemonSet":
		return w.daemonSet(ctx, action.Target{
			Kind: "DaemonSet", Namespace: target.Namespace, Name: owner.Name,
		})

	default:
		// A Job's pod, or something custom. No replica count to reason
		// about, so the guard has nothing to say.
		return guards.Workload{}, false, nil
	}
}

func (w *WorkloadHealth) get(ctx context.Context, target action.Target, into client.Object) error {
	if w.Reader == nil {
		return fmt.Errorf("no client")
	}
	key := client.ObjectKey{Namespace: target.Namespace, Name: target.Name}
	if err := w.Reader.Get(ctx, key, into); err != nil {
		return fmt.Errorf("read %s: %w", target, err)
	}
	return nil
}

func controllerOf(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}

func replicasOf(n *int32) int32 {
	if n == nil {
		return 1
	}
	return *n
}
