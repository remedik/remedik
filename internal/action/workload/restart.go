// Package workload implements remediation actions that operate on
// Kubernetes workloads.
package workload

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

// RestartAnnotation is the annotation patched onto a pod template to
// trigger a rolling restart. It is the same key `kubectl rollout restart`
// uses, so a remediation looks like what an operator would have done by
// hand, and the two do not fight each other.
const RestartAnnotation = "kubectl.kubernetes.io/restartedAt"

// The alert labels a target is resolved from when the step names none.
//
// The kubernetes-mixin alerts carry the object's name under a label named
// after its kind, so the label that is present is also the answer to "which
// kind is this?".
const (
	LabelNamespace   = "namespace"
	LabelDeployment  = "deployment"
	LabelStatefulSet = "statefulset"
	LabelDaemonSet   = "daemonset"
)

// KindParam lets a step name the workload kind explicitly, for an alert
// that carries none of the labels above.
const KindParam = "kind"

// NameParam lets a step name the workload explicitly.
const NameParam = "name"

// Kinds this action understands, in the order Resolve looks for them.
var workloadKinds = []struct {
	kind  string
	label string
}{
	{kind: "Deployment", label: LabelDeployment},
	{kind: "StatefulSet", label: LabelStatefulSet},
	{kind: "DaemonSet", label: LabelDaemonSet},
}

// Restart performs a rolling restart of a workload controller.
//
// It never deletes pods: patching the pod template hands the rollout to the
// workload's own controller, which respects maxUnavailable, readiness
// probes and PodDisruptionBudgets. Deleting pods would bypass all three.
type Restart struct {
	client client.Client
	now    func() time.Time
	poll   time.Duration

	// name is the verb this instance is registered under.
	name string
	// pinnedKind, when set, is the only kind this instance acts on. It is
	// what keeps `deployment.restart` meaning exactly what it always did,
	// and what keeps its RBAC to Deployments alone.
	pinnedKind string
}

// DefaultVerifyPoll is how often verification re-reads the workload while it
// waits. Rollouts move in seconds, and a tighter loop would only spend the
// API server's budget to learn the same thing.
const DefaultVerifyPoll = 2 * time.Second

// NewDeploymentRestart builds the Deployment-only restart. It exists
// unchanged so that strategies written against `deployment.restart` keep
// working, and so that enabling it grants permission on Deployments alone.
func NewDeploymentRestart(c client.Client, now func() time.Time) *Restart {
	return &Restart{
		client:     c,
		now:        orNow(now),
		poll:       DefaultVerifyPoll,
		name:       "deployment.restart",
		pinnedKind: "Deployment",
	}
}

// NewWorkloadRestart builds the restart that handles every workload kind.
func NewWorkloadRestart(c client.Client, now func() time.Time) *Restart {
	return &Restart{
		client: c,
		now:    orNow(now),
		poll:   DefaultVerifyPoll,
		name:   "workload.restart",
	}
}

func orNow(now func() time.Time) func() time.Time {
	if now == nil {
		return time.Now
	}
	return now
}

// Name implements action.Action.
func (a *Restart) Name() string { return a.name }

// Resolve determines the workload from the alert's labels, or from the
// step's parameters when given.
//
// Alerts about a pod usually carry the pod name rather than its owner. This
// action does not guess the workload from a pod name — deriving an owner
// from a name pattern is exactly the kind of guess that restarts the wrong
// thing — so the alert must carry a workload label, or the strategy must
// name one.
func (a *Restart) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	namespace := params.Get(LabelNamespace, labels[LabelNamespace])
	if namespace == "" {
		return action.Target{}, fmt.Errorf(
			"no namespace: the alert has no %q label and the step sets no %q parameter",
			LabelNamespace, LabelNamespace)
	}

	kind, name, err := a.workload(labels, params)
	if err != nil {
		return action.Target{}, err
	}
	return action.Target{Kind: kind, Namespace: namespace, Name: name}, nil
}

// workload picks the kind and name, preferring what the step says.
func (a *Restart) workload(labels map[string]string, params action.Params) (kind, name string, err error) {
	// A pinned instance only ever acts on its own kind, whatever the alert
	// happens to carry.
	if a.pinnedKind != "" {
		label := labelFor(a.pinnedKind)
		name = params.Get(NameParam, params.Get(label, labels[label]))
		if name == "" {
			return "", "", fmt.Errorf(
				"no %s: the alert has no %q label and the step sets no %q or %q parameter",
				lower(a.pinnedKind), label, NameParam, label)
		}
		return a.pinnedKind, name, nil
	}

	if named := params.Get(KindParam, ""); named != "" {
		kind, ok := canonicalKind(named)
		if !ok {
			return "", "", fmt.Errorf("unsupported %s %q: want one of Deployment, StatefulSet or DaemonSet",
				KindParam, named)
		}
		name = params.Get(NameParam, params.Get(labelFor(kind), labels[labelFor(kind)]))
		if name == "" {
			return "", "", fmt.Errorf("no name: the step sets %s=%s but neither %q nor the %q label",
				KindParam, named, NameParam, labelFor(kind))
		}
		return kind, name, nil
	}

	// Whichever workload label the alert carries names both the object and
	// its kind.
	for _, candidate := range workloadKinds {
		if value := params.Get(candidate.label, labels[candidate.label]); value != "" {
			return candidate.kind, value, nil
		}
	}

	return "", "", fmt.Errorf(
		"no workload: the alert carries none of the %q, %q or %q labels, and the step sets no %q and %q. "+
			"An alert naming only a pod is not enough: guessing an owner from a pod name is how the wrong "+
			"workload gets restarted",
		LabelDeployment, LabelStatefulSet, LabelDaemonSet, KindParam, NameParam)
}

// Plan reports what Execute would do, and reads the workload so that dry-run
// surfaces a missing target instead of reporting success for something that
// could never work.
func (a *Restart) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	target := req.Target
	object, err := a.fetch(ctx, target)
	if err != nil {
		return action.Result{}, err
	}

	state := readRollout(object)
	result := action.Result{
		Summary: fmt.Sprintf("restart %s (%d %s) by patching %s on its pod template",
			target, state.desired, state.unit, RestartAnnotation),
		Kubectl: kubectlRestart(target),
	}
	result.Output("replicas", strconv.Itoa(int(state.desired)))
	result.Output("readyReplicas", strconv.Itoa(int(state.ready)))
	return result, nil
}

// Execute triggers the rolling restart.
func (a *Restart) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	target := req.Target
	object, err := a.fetch(ctx, target)
	if err != nil {
		return action.Result{}, err
	}

	stamp := a.now().UTC().Format(time.RFC3339)
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{%q:%q}}}}}`,
		RestartAnnotation, stamp))

	if err := a.client.Patch(ctx, object, client.RawPatch(types.StrategicMergePatchType, patch)); err != nil {
		if apierrors.IsForbidden(err) {
			return action.Result{}, fmt.Errorf("not permitted to patch %s: %w", target, err)
		}
		return action.Result{}, fmt.Errorf("patch %s: %w", target, err)
	}

	result := action.Result{
		Summary: fmt.Sprintf("restarted %s: set %s=%s (resourceVersion %s)",
			target, RestartAnnotation, stamp, object.GetResourceVersion()),
		Kubectl: kubectlRestart(target),
	}
	result.Output("restartedAt", stamp)
	result.Output("replicas", strconv.Itoa(int(readRollout(object).desired)))
	result.Output("resourceVersion", object.GetResourceVersion())
	return result, nil
}

// Verify waits for the rollout to finish.
//
// Patching the annotation only means the API server accepted it. The
// question an operator is actually asking — did the workload come back? — is
// answered by the controller's status, and only after it has observed the
// new generation. Reporting success on the patch alone is reporting on the
// wrong event.
func (a *Restart) Verify(
	ctx context.Context, req action.Request, _ action.Result,
) (action.Result, error) {
	target := req.Target
	ctx, cancel := action.WithVerifyDeadline(ctx)
	defer cancel()

	var last string

	for {
		object, err := a.fetch(ctx, target)
		if err != nil {
			return action.Result{Summary: last}, err
		}

		state := readRollout(object)
		says, done := state.describe()
		last = says
		if done {
			result := action.Result{Summary: says}
			result.Output("readyReplicas", strconv.Itoa(int(state.ready)))
			result.Output("updatedReplicas", strconv.Itoa(int(state.updated)))
			return result, nil
		}

		select {
		case <-ctx.Done():
			// The deadline is the step's verifyTimeout. Saying what the
			// rollout had reached when time ran out is more useful than
			// saying that time ran out.
			return action.Result{Summary: says},
				fmt.Errorf("the rollout did not complete in time: %s", says)
		case <-time.After(a.poll):
		}
	}
}

// fetch reads the workload as the right concrete type.
func (a *Restart) fetch(ctx context.Context, target action.Target) (client.Object, error) {
	object, err := emptyWorkload(target.Kind)
	if err != nil {
		return nil, err
	}

	key := client.ObjectKey{Namespace: target.Namespace, Name: target.Name}
	if err := a.client.Get(ctx, key, object); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return nil, fmt.Errorf("%s does not exist: %w", target, err)
		case apierrors.IsForbidden(err):
			return nil, fmt.Errorf("not permitted to read %s: %w", target, err)
		default:
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
	}
	return object, nil
}

func emptyWorkload(kind string) (client.Object, error) {
	canonical, ok := canonicalKind(kind)
	if !ok {
		return nil, fmt.Errorf("unsupported workload kind %q", kind)
	}
	switch canonical {
	case "Deployment":
		return &appsv1.Deployment{}, nil
	case "StatefulSet":
		return &appsv1.StatefulSet{}, nil
	default:
		return &appsv1.DaemonSet{}, nil
	}
}

// rollout is how far a workload's controller has got, in the vocabulary the
// three kinds happen to share. DaemonSets count nodes rather than replicas,
// which is the same question asked of a different denominator.
type rollout struct {
	generation int64
	observed   int64
	desired    int32
	updated    int32
	current    int32
	available  int32
	ready      int32
	// unit is what the counts count. A DaemonSet has one pod per node, so
	// calling them replicas would be describing it in someone else's
	// vocabulary.
	unit string
}

func readRollout(object client.Object) rollout {
	switch o := object.(type) {
	case *appsv1.Deployment:
		desired := int32(1)
		if o.Spec.Replicas != nil {
			desired = *o.Spec.Replicas
		}
		return rollout{
			generation: o.Generation, observed: o.Status.ObservedGeneration,
			desired: desired, updated: o.Status.UpdatedReplicas,
			current: o.Status.Replicas, available: o.Status.AvailableReplicas,
			ready: o.Status.ReadyReplicas, unit: "replicas",
		}
	case *appsv1.StatefulSet:
		desired := int32(1)
		if o.Spec.Replicas != nil {
			desired = *o.Spec.Replicas
		}
		return rollout{
			generation: o.Generation, observed: o.Status.ObservedGeneration,
			desired: desired, updated: o.Status.UpdatedReplicas,
			current: o.Status.Replicas, available: o.Status.AvailableReplicas,
			ready: o.Status.ReadyReplicas, unit: "replicas",
		}
	case *appsv1.DaemonSet:
		return rollout{
			generation: o.Generation, observed: o.Status.ObservedGeneration,
			desired: o.Status.DesiredNumberScheduled, updated: o.Status.UpdatedNumberScheduled,
			current: o.Status.CurrentNumberScheduled, available: o.Status.NumberAvailable,
			ready: o.Status.NumberReady, unit: "nodes",
		}
	default:
		return rollout{unit: "replicas"}
	}
}

// describe says how far the rollout has got, and whether it is done.
//
// The generation check comes first and matters most: without it, a status
// left over from before the patch reads as a completed rollout, and the
// verification passes before anything has happened at all.
func (r rollout) describe() (string, bool) {
	if r.observed < r.generation {
		return fmt.Sprintf("waiting for the controller to observe generation %d (at %d)",
			r.generation, r.observed), false
	}

	unit := r.unit
	if unit == "" {
		unit = "replicas"
	}

	switch {
	case r.updated < r.desired:
		return fmt.Sprintf("%d/%d %s updated", r.updated, r.desired, unit), false
	case r.current > r.updated:
		return fmt.Sprintf("%d old %s still terminating", r.current-r.updated, unit), false
	case r.available < r.desired:
		return fmt.Sprintf("%d/%d %s available", r.available, r.desired, unit), false
	default:
		return fmt.Sprintf("%d/%d %s updated, available and ready",
			r.ready, r.desired, unit), true
	}
}

// kubectlRestart is the command a human would have typed. It is recorded,
// never executed.
func kubectlRestart(target action.Target) string {
	return fmt.Sprintf("kubectl rollout restart %s/%s -n %s",
		lower(target.Kind), target.Name, target.Namespace)
}

// canonicalKind accepts the way people write a kind — "statefulset",
// "StatefulSet", "sts" — and returns the one spelling the rest of the code
// uses.
func canonicalKind(kind string) (string, bool) {
	switch lower(kind) {
	case "deployment", "deploy", "deployments":
		return "Deployment", true
	case "statefulset", "sts", "statefulsets":
		return "StatefulSet", true
	case "daemonset", "ds", "daemonsets":
		return "DaemonSet", true
	default:
		return "", false
	}
}

func labelFor(kind string) string {
	switch kind {
	case "StatefulSet":
		return LabelStatefulSet
	case "DaemonSet":
		return LabelDaemonSet
	default:
		return LabelDeployment
	}
}

// Compile-time proof that the action satisfies the contract.
var (
	_ action.Action   = (*Restart)(nil)
	_ action.Verifier = (*Restart)(nil)
)

// lower is strings.ToLower, named for what the callers mean by it: the way
// a kind is written in a target string and on a kubectl command line.
func lower(s string) string { return strings.ToLower(s) }

// parseNonNegativeInt reads a whole number of seconds, rejecting anything
// that would silently become zero.
func parseNonNegativeInt(raw string) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a whole number", raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("%d is negative", n)
	}
	return n, nil
}

// itoa32 formats a counter for a structured output.
func itoa32(n int32) string { return strconv.Itoa(int(n)) }
