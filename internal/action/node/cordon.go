// Package node implements the remediation actions that operate on nodes.
//
// They are separate from the workload actions because they are a different
// shape: cluster-scoped, and reasoning about pods across every namespace
// rather than one object in one. Keeping them apart keeps each package
// honest about what it reaches.
//
// They are also the highest-risk verbs in the catalogue, and they landed
// last on purpose — after the contract could verify its own work, and after
// a guard existed that could bound them.
package node

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

// LabelNode is the alert label naming the node.
const LabelNode = "node"

// NameParam lets a step name the node explicitly.
const NameParam = "name"

// DefaultPoll is how often a verification re-reads the cluster.
const DefaultPoll = 2 * time.Second

// mergePatch is the content type for the patches these actions send.
const mergePatch = types.MergePatchType

// Cordon marks a node schedulable or not.
//
// Cordoning is the safest action in Kubernetes and the right first response
// to almost every node alert: nothing moves, nothing restarts, new work goes
// elsewhere, and one command undoes it. That a tool holding write access to
// a cluster could restart workloads and evict pods but not do the reversible
// thing was the wrong shape.
type Cordon struct {
	client client.Client
	poll   time.Duration

	// unschedulable is what this instance sets. One implementation, two
	// verbs: the difference between cordon and uncordon is a boolean, and
	// pretending otherwise would be two copies of the same code.
	unschedulable bool
	name          string
}

// NewCordon builds the action that stops new work landing on a node.
func NewCordon(c client.Client) *Cordon {
	return &Cordon{client: c, poll: DefaultPoll, unschedulable: true, name: "node.cordon"}
}

// NewUncordon builds the undo. An automation with no reverse gear does not
// get installed twice.
func NewUncordon(c client.Client) *Cordon {
	return &Cordon{client: c, poll: DefaultPoll, unschedulable: false, name: "node.uncordon"}
}

// Name implements action.Action.
func (a *Cordon) Name() string { return a.name }

// Resolve determines the node from the alert's `node` label.
func (a *Cordon) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	return resolveNode(labels, params)
}

// Plan reports what Execute would do, and reads the node so a dry run
// surfaces one that is already in the wanted state.
func (a *Cordon) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	node, err := a.fetch(ctx, req.Target)
	if err != nil {
		return action.Result{}, err
	}
	return a.describe(req.Target, node), nil
}

// Execute sets the node's schedulability.
func (a *Cordon) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	node, err := a.fetch(ctx, req.Target)
	if err != nil {
		return action.Result{}, err
	}

	// Already there. Reporting that as a failure would make a strategy
	// unusable the second time an alert fires, which is every time.
	if node.Spec.Unschedulable == a.unschedulable {
		result := a.describe(req.Target, node)
		result.Output("changed", "false")
		return result, nil
	}

	patch := []byte(fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, a.unschedulable))
	if err := a.client.Patch(ctx, node, client.RawPatch(mergePatch, patch)); err != nil {
		if apierrors.IsForbidden(err) {
			return action.Result{}, fmt.Errorf("not permitted to patch %s: %w", req.Target, err)
		}
		return action.Result{}, fmt.Errorf("patch %s: %w", req.Target, err)
	}

	result := a.describe(req.Target, node)
	result.Output("changed", "true")
	return result, nil
}

// Verify reads the node back.
func (a *Cordon) Verify(
	ctx context.Context, req action.Request, _ action.Result,
) (action.Result, error) {
	ctx, cancel := action.WithVerifyDeadline(ctx)
	defer cancel()

	for {
		node, err := a.fetch(ctx, req.Target)
		if err != nil {
			return action.Result{}, err
		}
		if node.Spec.Unschedulable == a.unschedulable {
			return action.Result{Summary: fmt.Sprintf("%s is %s", req.Target, a.state())}, nil
		}

		select {
		case <-ctx.Done():
			return action.Result{Summary: fmt.Sprintf("%s is still %s", req.Target, a.oppositeState())},
				fmt.Errorf("%s did not become %s in time", req.Target, a.state())
		case <-time.After(a.poll):
		}
	}
}

func (a *Cordon) describe(target action.Target, node *corev1.Node) action.Result {
	verb := "cordon"
	if !a.unschedulable {
		verb = "uncordon"
	}

	result := action.Result{
		Summary: fmt.Sprintf("%s %s: it is currently %s", verb, target, a.currentState(node)),
		Kubectl: fmt.Sprintf("kubectl %s %s", verb, target.Name),
	}
	result.Output("unschedulable", strconv.FormatBool(node.Spec.Unschedulable))
	result.Output("ready", strconv.FormatBool(isReady(node)))
	return result
}

func (a *Cordon) state() string {
	if a.unschedulable {
		return "unschedulable"
	}
	return "schedulable"
}

func (a *Cordon) oppositeState() string {
	if a.unschedulable {
		return "schedulable"
	}
	return "unschedulable"
}

func (a *Cordon) currentState(node *corev1.Node) string {
	if node.Spec.Unschedulable {
		return "unschedulable"
	}
	return "schedulable"
}

func (a *Cordon) fetch(ctx context.Context, target action.Target) (*corev1.Node, error) {
	return fetchNode(ctx, a.client, target)
}

// resolveNode is shared by every action in this package.
func resolveNode(labels map[string]string, params action.Params) (action.Target, error) {
	name := params.Get(NameParam, params.Get(LabelNode, labels[LabelNode]))
	if name == "" {
		return action.Target{}, fmt.Errorf(
			"no node: the alert has no %q label and the step sets no %q parameter",
			LabelNode, NameParam)
	}
	// Nodes are cluster-scoped, so the target carries no namespace.
	return action.Target{Kind: "Node", Name: name}, nil
}

func fetchNode(ctx context.Context, c client.Client, target action.Target) (*corev1.Node, error) {
	var node corev1.Node
	if err := c.Get(ctx, client.ObjectKey{Name: target.Name}, &node); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return nil, fmt.Errorf("%s does not exist: %w", target, err)
		case apierrors.IsForbidden(err):
			return nil, fmt.Errorf("not permitted to read %s: %w", target, err)
		default:
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
	}
	return &node, nil
}

func isReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Compile-time proof that the action satisfies the contract.
var (
	_ action.Action   = (*Cordon)(nil)
	_ action.Verifier = (*Cordon)(nil)
)
