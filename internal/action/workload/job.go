package workload

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

// LabelJob is the alert label naming the Job.
//
// It is `job_name`, not `job`, and the difference matters: in Prometheus
// `job` is the *scrape* job, so nearly every alert carries one — with a
// value like "kube-state-metrics". Accepting it as a fallback would mean a
// misconfigured alert quietly resolving to a Job that does not exist, or
// worse, one that does. A step that needs to name the Job by hand uses the
// `job` parameter, where it was written deliberately.
const (
	LabelJob         = "job_name"
	JobParam         = "job"
	PropagationParam = "propagationPolicy"
)

// JobDelete removes a Job so the CronJob that owns it makes a clean one.
//
// A failed Job stays failed: nothing retries it, and its pods sit there
// holding their logs and their resources until someone notices. Deleting it
// is what the schedule needs to produce a fresh attempt.
type JobDelete struct {
	client client.Client
	poll   time.Duration
}

// NewJobDelete builds the action.
func NewJobDelete(c client.Client) *JobDelete {
	return &JobDelete{client: c, poll: DefaultVerifyPoll}
}

// Name implements action.Action.
func (a *JobDelete) Name() string { return "job.delete" }

// Resolve determines the Job from the alert's labels or the step's
// parameters.
func (a *JobDelete) Resolve(labels map[string]string, params action.Params) (action.Target, error) {
	namespace := params.Get(LabelNamespace, labels[LabelNamespace])
	name := params.Get(LabelJob, params.Get(JobParam, labels[LabelJob]))

	if namespace == "" {
		return action.Target{}, fmt.Errorf(
			"no namespace: the alert has no %q label and the step sets no %q parameter",
			LabelNamespace, LabelNamespace)
	}
	if name == "" {
		return action.Target{}, fmt.Errorf(
			"no job: the alert has no %q label and the step sets no %q parameter. "+
				"The Prometheus %q label is the scrape job, not the Kubernetes Job, so it is "+
				"deliberately not used here",
			LabelJob, JobParam, JobParam)
	}

	return action.Target{Kind: "Job", Namespace: namespace, Name: name}, nil
}

// Plan reports what Execute would do, and reads the Job so a dry run
// surfaces one that has already gone.
func (a *JobDelete) Plan(ctx context.Context, target action.Target, params action.Params) (action.Result, error) {
	job, err := a.fetch(ctx, target)
	if err != nil {
		return action.Result{}, err
	}
	if _, err := propagation(params); err != nil {
		return action.Result{}, err
	}

	result := action.Result{
		Summary: fmt.Sprintf("delete %s and its pods, so %s creates a fresh run",
			target, jobOwner(job)),
		Kubectl: kubectlDeleteJob(target),
	}
	result.Output("owner", jobOwner(job))
	result.Output("failed", itoa32(job.Status.Failed))
	result.Output("succeeded", itoa32(job.Status.Succeeded))
	return result, nil
}

// Execute deletes the Job.
func (a *JobDelete) Execute(ctx context.Context, target action.Target, params action.Params) (action.Result, error) {
	job, err := a.fetch(ctx, target)
	if err != nil {
		return action.Result{}, err
	}

	policy, err := propagation(params)
	if err != nil {
		return action.Result{}, err
	}

	if err := a.client.Delete(ctx, job, client.PropagationPolicy(policy)); err != nil {
		switch {
		case apierrors.IsForbidden(err):
			return action.Result{}, fmt.Errorf("not permitted to delete %s: %w", target, err)
		case apierrors.IsNotFound(err):
			return action.Result{}, fmt.Errorf("%s went away before it could be deleted: %w", target, err)
		default:
			return action.Result{}, fmt.Errorf("delete %s: %w", target, err)
		}
	}

	result := action.Result{
		Summary: fmt.Sprintf("deleted %s (%d failed pods), propagating to its pods", target, job.Status.Failed),
		Kubectl: kubectlDeleteJob(target),
	}
	result.Output("owner", jobOwner(job))
	result.Output("propagationPolicy", string(policy))
	result.Output("uid", string(job.UID))
	return result, nil
}

// Verify waits for the Job to go.
//
// Background propagation returns as soon as the deletion is recorded, not
// when it has finished; a Job with a finalizer, or with pods that will not
// terminate, can outlive the call by a long way.
func (a *JobDelete) Verify(
	ctx context.Context, target action.Target, _ action.Params, _ action.Result,
) (action.Result, error) {
	ctx, cancel := action.WithVerifyDeadline(ctx)
	defer cancel()

	for {
		job, err := a.fetch(ctx, target)
		switch {
		case apierrors.IsNotFound(err):
			result := action.Result{Summary: fmt.Sprintf("%s is gone", target)}
			result.Output("outcome", "deleted")
			return result, nil
		case err != nil:
			return action.Result{}, err
		}

		says := fmt.Sprintf("%s still exists", target)
		if job.DeletionTimestamp != nil {
			says = fmt.Sprintf("%s is terminating", target)
		}

		select {
		case <-ctx.Done():
			return action.Result{Summary: says}, fmt.Errorf("the job did not go away in time: %s", says)
		case <-time.After(a.poll):
		}
	}
}

func (a *JobDelete) fetch(ctx context.Context, target action.Target) (*batchv1.Job, error) {
	var job batchv1.Job
	key := client.ObjectKey{Namespace: target.Namespace, Name: target.Name}

	if err := a.client.Get(ctx, key, &job); err != nil {
		switch {
		case apierrors.IsNotFound(err):
			return nil, fmt.Errorf("%s does not exist: %w", target, err)
		case apierrors.IsForbidden(err):
			return nil, fmt.Errorf("not permitted to read %s: %w", target, err)
		default:
			return nil, fmt.Errorf("read %s: %w", target, err)
		}
	}
	return &job, nil
}

// propagation reads the deletion policy.
//
// Background is the default because the point of deleting a failed Job is to
// clear it and its pods away; Orphan would leave the pods holding their
// resources with nothing owning them, which is a worse state than the one
// being remediated.
func propagation(params action.Params) (metav1.DeletionPropagation, error) {
	switch value := params.Get(PropagationParam, "Background"); value {
	case "Background":
		return metav1.DeletePropagationBackground, nil
	case "Foreground":
		return metav1.DeletePropagationForeground, nil
	case "Orphan":
		return metav1.DeletePropagationOrphan, nil
	default:
		return "", fmt.Errorf("parameter %q: %q is not one of Background, Foreground or Orphan",
			PropagationParam, value)
	}
}

func jobOwner(job *batchv1.Job) string {
	for _, ref := range job.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return fmt.Sprintf("%s/%s", lower(ref.Kind), ref.Name)
		}
	}
	return "nothing"
}

func kubectlDeleteJob(target action.Target) string {
	return fmt.Sprintf("kubectl delete job %s -n %s", target.Name, target.Namespace)
}

// Compile-time proof that the action satisfies the contract.
var (
	_ action.Action   = (*JobDelete)(nil)
	_ action.Verifier = (*JobDelete)(nil)
)
