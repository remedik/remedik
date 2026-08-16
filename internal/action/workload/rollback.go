package workload

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/internal/action"
)

// RevisionAnnotation is where the Deployment controller records which
// revision a ReplicaSet holds. `kubectl rollout undo` reads the same one, so
// remedik and a person agree about what "the previous revision" means.
const RevisionAnnotation = "deployment.kubernetes.io/revision"

// Parameters of deployment.rollback.
const (
	// ToRevisionParam names the revision to return to. Unset means the one
	// before the current.
	ToRevisionParam = "toRevision"
	// IgnoreGitOpsParam overrides the refusal to roll back something a
	// GitOps controller manages.
	IgnoreGitOpsParam = "ignoreGitOps"
)

// gitOpsMarkers are the labels and annotations Argo CD and Flux actually set
// on what they manage. Each entry is the key and the controller it names.
var gitOpsMarkers = []struct {
	key        string
	annotation bool
	controller string
}{
	{key: "argocd.argoproj.io/instance", annotation: true, controller: "Argo CD"},
	{key: "app.kubernetes.io/instance", controller: "Argo CD"},
	{key: "kustomize.toolkit.fluxcd.io/name", controller: "Flux"},
	{key: "helm.toolkit.fluxcd.io/name", controller: "Flux"},
}

// DeploymentRollback puts back the previous version of a Deployment.
//
// It is the highest-value action in the catalogue, because the most common
// cause of a crash loop at 3am is the deploy at 2:50, and it is the one most
// likely to surprise somebody — in a GitOps cluster the controller reverts
// it within minutes, and the operator sees remedik report success while the
// outage continues. That is why the refusal below exists.
type DeploymentRollback struct {
	client client.Client
	poll   time.Duration
}

// NewDeploymentRollback builds the action.
func NewDeploymentRollback(c client.Client) *DeploymentRollback {
	return &DeploymentRollback{client: c, poll: DefaultVerifyPoll}
}

// Name implements action.Action.
func (a *DeploymentRollback) Name() string { return "deployment.rollback" }

// Resolve determines the Deployment, the same way deployment.restart does.
func (a *DeploymentRollback) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	namespace := params.Get(LabelNamespace, labels[LabelNamespace])
	name := params.Get(NameParam, params.Get(LabelDeployment, labels[LabelDeployment]))

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

// Plan reports what Execute would do, performing every check Execute performs
// so a dry run cannot promise a rollback that would be refused.
func (a *DeploymentRollback) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	deployment, target, err := a.rollbackTarget(ctx, req)
	if err != nil {
		return action.Result{}, err
	}

	result := action.Result{
		Summary: fmt.Sprintf("roll %s back from revision %d to revision %d",
			req.Target, target.current, target.revision),
		Kubectl: kubectlRollback(req.Target, target.revision),
	}
	result.Output("currentRevision", strconv.FormatInt(target.current, 10))
	result.Output("toRevision", strconv.FormatInt(target.revision, 10))
	result.Output("replicas", strconv.Itoa(int(readRollout(deployment).desired)))
	return result, nil
}

// Execute puts the previous revision's pod template back.
func (a *DeploymentRollback) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	deployment, target, err := a.rollbackTarget(ctx, req)
	if err != nil {
		return action.Result{}, err
	}

	// The same thing `kubectl rollout undo` does: take the old ReplicaSet's
	// pod template and make it the Deployment's again. The controller does
	// the rest, honouring maxUnavailable and readiness as it would for any
	// other change.
	template := target.replicaSet.Spec.Template.DeepCopy()
	delete(template.Labels, "pod-template-hash")

	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{"template": template},
	})
	if err != nil {
		return action.Result{}, fmt.Errorf("build the rollback patch: %w", err)
	}

	if err := a.client.Patch(ctx, deployment, client.RawPatch(mergePatch, patch)); err != nil {
		if apierrors.IsForbidden(err) {
			return action.Result{}, fmt.Errorf("not permitted to update %s: %w", req.Target, err)
		}
		return action.Result{}, fmt.Errorf("roll %s back: %w", req.Target, err)
	}

	result := action.Result{
		Summary: fmt.Sprintf("rolled %s back to revision %d (from %d)",
			req.Target, target.revision, target.current),
		Kubectl: kubectlRollback(req.Target, target.revision),
	}
	result.Output("rolledBackTo", strconv.FormatInt(target.revision, 10))
	result.Output("fromRevision", strconv.FormatInt(target.current, 10))
	result.Output("replicaSet", target.replicaSet.Name)
	return result, nil
}

// Verify waits for the rollout, exactly as a restart does: the question is
// whether the old version came back up, not whether the patch was accepted.
func (a *DeploymentRollback) Verify(
	ctx context.Context, req action.Request, _ action.Result,
) (action.Result, error) {
	ctx, cancel := action.WithVerifyDeadline(ctx)
	defer cancel()

	// A fully-formed Restart, clock included: the verification path does not
	// read the clock today, and a struct that would panic if it ever did is
	// a trap for whoever changes it next.
	restart := &Restart{
		client: a.client, now: time.Now, poll: a.poll,
		name: a.Name(), pinnedKind: "Deployment",
	}
	return restart.Verify(ctx, req, action.Result{})
}

// rollback is the revision this action would return to.
type rollback struct {
	current    int64
	revision   int64
	replicaSet *appsv1.ReplicaSet
}

func (a *DeploymentRollback) rollbackTarget(
	ctx context.Context, req action.Request,
) (*appsv1.Deployment, rollback, error) {
	var deployment appsv1.Deployment
	key := client.ObjectKey{Namespace: req.Target.Namespace, Name: req.Target.Name}
	if err := a.client.Get(ctx, key, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, rollback{}, fmt.Errorf("%s does not exist: %w", req.Target, err)
		}
		return nil, rollback{}, fmt.Errorf("read %s: %w", req.Target, err)
	}

	if err := checkGitOps(&deployment, req); err != nil {
		return nil, rollback{}, err
	}

	current := revisionOf(deployment.Annotations)

	var sets appsv1.ReplicaSetList
	if err := a.client.List(ctx, &sets, client.InNamespace(req.Target.Namespace)); err != nil {
		return nil, rollback{}, fmt.Errorf("list the revisions of %s: %w", req.Target, err)
	}

	owned := ownedReplicaSets(&deployment, &sets)
	if len(owned) == 0 {
		return nil, rollback{}, fmt.Errorf(
			"%s has no revision history to roll back to: its ReplicaSets are gone, "+
				"which usually means revisionHistoryLimit is 0", req.Target)
	}

	// Newest first, so "the previous revision" is the second entry.
	sort.Slice(owned, func(i, j int) bool {
		return revisionOf(owned[i].Annotations) > revisionOf(owned[j].Annotations)
	})

	wanted, err := requestedRevision(req.Params)
	if err != nil {
		return nil, rollback{}, err
	}

	if wanted == 0 {
		for _, rs := range owned {
			if revision := revisionOf(rs.Annotations); revision < current {
				return &deployment, rollback{current: current, revision: revision, replicaSet: rs}, nil
			}
		}
		return nil, rollback{}, fmt.Errorf(
			"%s is at revision %d and has no earlier one kept: there is nothing to roll back to",
			req.Target, current)
	}

	for _, rs := range owned {
		if revisionOf(rs.Annotations) == wanted {
			if wanted == current {
				return nil, rollback{}, fmt.Errorf(
					"%s is already at revision %d", req.Target, wanted)
			}
			return &deployment, rollback{current: current, revision: wanted, replicaSet: rs}, nil
		}
	}
	return nil, rollback{}, fmt.Errorf(
		"%s has no revision %d kept; available: %s", req.Target, wanted, revisionList(owned))
}

// checkGitOps refuses a workload a GitOps controller reconciles.
//
// A rollback that Argo CD or Flux undoes within minutes is worse than no
// rollback: remedik records Succeeded, the outage continues, and the
// incident spends its time discovering that two systems are fighting.
func checkGitOps(deployment *appsv1.Deployment, req action.Request) error {
	if req.Params.Get(IgnoreGitOpsParam, "false") == "true" {
		return nil
	}

	for _, marker := range gitOpsMarkers {
		source := deployment.Labels
		where := "label"
		if marker.annotation {
			source = deployment.Annotations
			where = "annotation"
		}
		if value := source[marker.key]; value != "" {
			return fmt.Errorf(
				"%s is managed by %s (%s %s=%s): a rollback would be reverted within minutes, "+
					"and remedik would have recorded a success while the outage continued. "+
					"Revert the commit instead, or set %s=\"true\" on the step if you know better",
				req.Target, marker.controller, where, marker.key, value, IgnoreGitOpsParam)
		}
	}
	return nil
}

// ownedReplicaSets keeps the ones this Deployment controls.
func ownedReplicaSets(d *appsv1.Deployment, sets *appsv1.ReplicaSetList) []*appsv1.ReplicaSet {
	var owned []*appsv1.ReplicaSet
	for i := range sets.Items {
		rs := &sets.Items[i]
		for _, ref := range rs.OwnerReferences {
			if ref.Controller != nil && *ref.Controller && ref.UID == d.UID {
				owned = append(owned, rs)
				break
			}
		}
	}
	return owned
}

func revisionOf(annotations map[string]string) int64 {
	n, err := strconv.ParseInt(annotations[RevisionAnnotation], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func revisionList(sets []*appsv1.ReplicaSet) string {
	out := make([]string, 0, len(sets))
	for _, rs := range sets {
		out = append(out, strconv.FormatInt(revisionOf(rs.Annotations), 10))
	}
	return joinComma(out)
}

func requestedRevision(params action.Params) (int64, error) {
	raw := params.Get(ToRevisionParam, "")
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("parameter %q: %q is not a revision number", ToRevisionParam, raw)
	}
	return n, nil
}

func kubectlRollback(target action.Target, revision int64) string {
	return fmt.Sprintf("kubectl rollout undo deployment/%s -n %s --to-revision=%d",
		target.Name, target.Namespace, revision)
}

// Compile-time proof that the action satisfies the contract.
var (
	_ action.Action   = (*DeploymentRollback)(nil)
	_ action.Verifier = (*DeploymentRollback)(nil)
)
