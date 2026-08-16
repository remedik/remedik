package workload

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/internal/action"
)

var jobTarget = action.Target{Kind: "Job", Namespace: "payments", Name: "nightly-billing-28471"}

// jobClient serves a scripted sequence of Job states and records deletions.
type jobClient struct {
	client.Client

	states []*batchv1.Job
	gets   int

	deleteErr   error
	deletes     int
	lastPolicy  metav1.DeletionPropagation
	lastDeleted string
}

func (c *jobClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return notFound(key.Name)
	}
	if len(c.states) == 0 {
		return apierrors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, key.Name)
	}
	index := min(c.gets, len(c.states)-1)
	c.gets++
	if c.states[index] == nil {
		return apierrors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, key.Name)
	}
	*job = *c.states[index]
	return nil
}

func (c *jobClient) Delete(_ context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.deletes++
	c.lastDeleted = obj.GetNamespace() + "/" + obj.GetName()

	var options client.DeleteOptions
	for _, opt := range opts {
		opt.ApplyToDelete(&options)
	}
	if options.PropagationPolicy != nil {
		c.lastPolicy = *options.PropagationPolicy
	}
	return c.deleteErr
}

func failedJob(name string, failures int32) *batchv1.Job {
	controller := true
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "payments", Name: name, UID: "job-1",
			OwnerReferences: []metav1.OwnerReference{{
				Kind: "CronJob", Name: "nightly-billing", Controller: &controller,
			}},
		},
		Status: batchv1.JobStatus{Failed: failures},
	}
}

func jobDeleter(states ...*batchv1.Job) (*JobDelete, *jobClient) {
	c := &jobClient{states: states}
	a := NewJobDelete(c)
	a.poll = time.Millisecond
	return a, c
}

func TestJobDelete_Resolve(t *testing.T) {
	a := NewJobDelete(&jobClient{})

	tests := []struct {
		name    string
		labels  map[string]string
		params  action.Params
		want    string
		wantErr string
	}{
		{
			// kube-state-metrics calls it job_name, because `job` already
			// means the scrape job in Prometheus.
			name:   "from the job_name label",
			labels: map[string]string{"namespace": "payments", "job_name": "nightly-billing-28471"},
			want:   "job/payments/nightly-billing-28471",
		},
		{
			// `job` on an alert is the Prometheus scrape job — usually
			// something like "kube-state-metrics". Treating it as a Kubernetes
			// Job name is how a remediation targets the wrong thing.
			name:    "the Prometheus job label is not a Job name",
			labels:  map[string]string{"namespace": "payments", "job": "kube-state-metrics"},
			wantErr: "no job",
		},
		{
			name:    "no label at all",
			labels:  map[string]string{"namespace": "payments"},
			wantErr: "no job",
		},
		{
			name:   "named by the step",
			labels: map[string]string{"namespace": "payments"},
			params: action.Params{JobParam: "nightly-billing-28471"},
			want:   "job/payments/nightly-billing-28471",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.Resolve(tc.labels, tc.params)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if got.String() != tc.want {
				t.Errorf("target = %q, want %q", got.String(), tc.want)
			}
		})
	}
}

func TestJobDelete_DeletesWithItsPods(t *testing.T) {
	a, c := jobDeleter(failedJob("nightly-billing-28471", 3))

	result, err := a.Execute(context.Background(), action.Request{Target: jobTarget, Params: nil})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if c.deletes != 1 {
		t.Fatalf("deleted %d times, want 1", c.deletes)
	}
	// Orphan would leave the pods holding resources with nothing owning
	// them: a worse state than the one being remediated.
	if c.lastPolicy != metav1.DeletePropagationBackground {
		t.Errorf("propagation = %q, want Background", c.lastPolicy)
	}
	if result.Outputs["owner"] != "cronjob/nightly-billing" {
		t.Errorf("outputs = %v, want the CronJob that will make a fresh run", result.Outputs)
	}
	if !strings.Contains(result.Kubectl, "kubectl delete job nightly-billing-28471") {
		t.Errorf("kubectl = %q", result.Kubectl)
	}
}

func TestJobDelete_RejectsAnUnknownPropagationPolicy(t *testing.T) {
	a, c := jobDeleter(failedJob("nightly-billing-28471", 1))

	for _, call := range []struct {
		name string
		run  func() (action.Result, error)
	}{
		{"Plan", func() (action.Result, error) {
			return a.Plan(context.Background(), action.Request{Target: jobTarget, Params: action.Params{PropagationParam: "Whenever"}})
		}},
		{"Execute", func() (action.Result, error) {
			return a.Execute(context.Background(), action.Request{Target: jobTarget, Params: action.Params{PropagationParam: "Whenever"}})
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			if _, err := call.run(); err == nil ||
				!strings.Contains(err.Error(), PropagationParam) {
				t.Errorf("error = %v, want it to name the bad parameter", err)
			}
		})
	}

	if c.deletes != 0 {
		t.Errorf("deleted %d jobs despite the bad parameter", c.deletes)
	}
}

func TestJobDelete_VerifyWaitsForTheJobToGo(t *testing.T) {
	a, _ := jobDeleter(failedJob("nightly-billing-28471", 3), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := a.Verify(ctx, action.Request{Target: jobTarget, Params: nil}, action.Result{})
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}
	if result.Outputs["outcome"] != "deleted" {
		t.Errorf("outputs = %v, want outcome deleted", result.Outputs)
	}
}

func TestJobDelete_VerifyFailsWhenTheJobLingers(t *testing.T) {
	stuck := failedJob("nightly-billing-28471", 3)
	deleting := metav1.NewTime(fixedClock)
	stuck.DeletionTimestamp = &deleting

	a, _ := jobDeleter(stuck)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	if _, err := a.Verify(ctx, action.Request{Target: jobTarget, Params: nil}, action.Result{}); err == nil {
		t.Fatal("Verify() error = nil; a Job held by a finalizer has not gone")
	}
}

func TestJobDelete_MissingJob(t *testing.T) {
	a, _ := jobDeleter()

	// Reporting success for deleting something that was not there would put
	// a Succeeded record next to a CronJob that still has not run.
	if _, err := a.Execute(context.Background(), action.Request{Target: jobTarget, Params: nil}); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %v, want a not-found error", err)
	}
}
