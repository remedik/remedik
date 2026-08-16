package external

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ratyx/remedik/internal/action"
)

var fixedClock = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

// jobClient records the Job that was created and serves scripted states back.
type jobClient struct {
	client.Client

	configMap *corev1.ConfigMap
	created   *batchv1.Job
	createErr error

	states []*batchv1.Job
	gets   int

	pods []corev1.Pod
}

func (c *jobClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	switch target := obj.(type) {
	case *corev1.ConfigMap:
		if c.configMap == nil || c.configMap.Name != key.Name {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, key.Name)
		}
		*target = *c.configMap
	case *batchv1.Job:
		if len(c.states) == 0 {
			return apierrors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, key.Name)
		}
		index := min(c.gets, len(c.states)-1)
		c.gets++
		*target = *c.states[index]
	default:
		return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
	}
	return nil
}

func (c *jobClient) Create(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
	if c.createErr != nil {
		return c.createErr
	}
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return errors.New("unexpected object")
	}
	// The API server turns GenerateName into a name; the fake does the same
	// so the action's outputs are realistic.
	job.Name = job.GenerateName + "abcde"
	c.created = job.DeepCopy()
	return nil
}

func (c *jobClient) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	pods, ok := list.(*corev1.PodList)
	if !ok {
		return errors.New("unexpected list")
	}
	pods.Items = append([]corev1.Pod(nil), c.pods...)
	return nil
}

// stubLogs serves a scripted tail.
type stubLogs struct {
	output string
	err    error
	reads  int
}

func (s *stubLogs) TailLogs(context.Context, string, string, int64) (string, error) {
	s.reads++
	return s.output, s.err
}

func jobAt(name string, succeeded, failed int32, done bool) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorNamespace, Name: name},
		Status:     batchv1.JobStatus{Succeeded: succeeded, Failed: failed},
	}
	if done && failed > 0 {
		job.Status.Conditions = []batchv1.JobCondition{{
			Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
		}}
	}
	return job
}

func jobRequest(params action.Params) action.Request {
	return action.Request{
		Params:      params,
		Labels:      map[string]string{"alertname": "KubeNodeNotReady", "node": "aks-np1-0003"},
		Remediation: "node-drain-x7k2q",
		Strategy:    "node-drain",
		Namespace:   operatorNamespace,
	}
}

func TestJobRun_CreatesTheJobWithTheIncidentInItsEnvironment(t *testing.T) {
	c := &jobClient{}
	a := NewJobRun(c, &stubLogs{}, "remedik", func() time.Time { return fixedClock })

	result, err := a.Execute(context.Background(), jobRequest(action.Params{
		ImageParam:          "ghcr.io/example/runbook:1",
		CommandParam:        `["/bin/sh","-c","echo hello"]`,
		ServiceAccountParam: "runbook-runner",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if c.created == nil {
		t.Fatal("no Job was created")
	}

	// The Job runs where remedik runs, so the permission stays namespaced.
	if c.created.Namespace != operatorNamespace {
		t.Errorf("Job namespace = %q, want %q", c.created.Namespace, operatorNamespace)
	}
	if got := c.created.Spec.Template.Spec.ServiceAccountName; got != "runbook-runner" {
		t.Errorf("ServiceAccount = %q, want the one the step named", got)
	}
	if got := c.created.Spec.Template.Spec.RestartPolicy; got != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want Never", got)
	}

	env := map[string]string{}
	for _, e := range c.created.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	// The prefix exists so an alert label called PATH cannot replace the
	// container's, which is the kind of thing that happens once and is never
	// diagnosed.
	if env["REMEDIK_ALERT_NODE"] != "aks-np1-0003" {
		t.Errorf("environment = %v, want the alert's labels prefixed", env)
	}
	if env["REMEDIK_STRATEGY"] != "node-drain" {
		t.Errorf("environment = %v, want the strategy", env)
	}
	if !strings.Contains(env["REMEDIK_ALERT_LABELS"], "aks-np1-0003") {
		t.Errorf("REMEDIK_ALERT_LABELS = %q, want the labels as JSON", env["REMEDIK_ALERT_LABELS"])
	}

	if result.Outputs["job"] == "" {
		t.Error("the result records no Job name, so verification has nothing to wait for")
	}
}

// A strategy author must not inherit every permission the operator holds by
// writing one word.
func TestJobRun_RefusesToRunAsRemedik(t *testing.T) {
	c := &jobClient{}
	a := NewJobRun(c, &stubLogs{}, "remedik", nil)

	_, err := a.Execute(context.Background(), jobRequest(action.Params{
		ImageParam:          "ghcr.io/example/runbook:1",
		CommandParam:        `["/bin/true"]`,
		ServiceAccountParam: "remedik",
	}))
	if err == nil {
		t.Fatal("error = nil; a Job must not run as remedik's own ServiceAccount")
	}
	if !strings.Contains(err.Error(), "own ServiceAccount") {
		t.Errorf("error = %q, want it to explain why", err)
	}
	if c.created != nil {
		t.Error("a Job was created despite the refusal")
	}
}

func TestJobRun_RefusesWhatItCannotRun(t *testing.T) {
	tests := []struct {
		name    string
		params  action.Params
		wantErr string
	}{
		{name: "no image", params: action.Params{CommandParam: `["/bin/true"]`}, wantErr: "no image"},
		{
			name:    "no command",
			params:  action.Params{ImageParam: "busybox"},
			wantErr: "no command",
		},
		{
			// A string command would need quoting rules, and quoting rules
			// invented for a YAML field are how a remediation runs something
			// nobody wrote.
			name:    "a command that is not a JSON array",
			params:  action.Params{ImageParam: "busybox", CommandParam: "/bin/sh -c 'echo hi'"},
			wantErr: "JSON array",
		},
		{
			name:    "an empty command array",
			params:  action.Params{ImageParam: "busybox", CommandParam: `[]`},
			wantErr: "empty",
		},
		{
			name:    "a backoff that is not a number",
			params:  action.Params{ImageParam: "busybox", CommandParam: `["/bin/true"]`, BackoffParam: "lots"},
			wantErr: "whole number",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &jobClient{}
			a := NewJobRun(c, &stubLogs{}, "remedik", nil)

			if _, err := a.Plan(context.Background(), jobRequest(tc.params)); err == nil ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Plan error = %v, want it to mention %q", err, tc.wantErr)
			}
			if _, err := a.Execute(context.Background(), jobRequest(tc.params)); err == nil ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Execute error = %v, want it to mention %q", err, tc.wantErr)
			}
			if c.created != nil {
				t.Error("a Job was created despite the refusal")
			}
		})
	}
}

func TestJobRun_VerifyWaitsForTheJobAndRecordsItsOutput(t *testing.T) {
	c := &jobClient{
		states: []*batchv1.Job{
			jobAt("remedik-node-drain-abcde", 0, 0, false), // still running
			jobAt("remedik-node-drain-abcde", 1, 0, false), // succeeded
		},
		pods: []corev1.Pod{{
			ObjectMeta: metav1.ObjectMeta{Name: "remedik-node-drain-abcde-pod"},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
			}}},
		}},
	}
	logs := &stubLogs{output: "drained 12 pods\ndone\n"}
	a := NewJobRun(c, logs, "remedik", nil)
	a.poll = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	executed := action.Result{Outputs: map[string]string{"job": "remedik-node-drain-abcde"}}
	result, err := a.Verify(ctx, jobRequest(nil), executed)
	if err != nil {
		t.Fatalf("Verify() error = %v, want nil", err)
	}
	if result.Outputs["jobOutcome"] != "Succeeded" {
		t.Errorf("outputs = %v, want jobOutcome Succeeded", result.Outputs)
	}
	// The tail of what the script said is most of why this action is worth
	// more than "kubectl create job".
	if !strings.Contains(result.Outputs["logs"], "drained 12 pods") {
		t.Errorf("outputs = %v, want the Job's output", result.Outputs)
	}
	if result.Outputs["exitCode"] != "0" {
		t.Errorf("outputs = %v, want the exit code", result.Outputs)
	}
}

func TestJobRun_AFailedJobFailsTheStep(t *testing.T) {
	c := &jobClient{
		states: []*batchv1.Job{jobAt("remedik-node-drain-abcde", 0, 1, true)},
		pods: []corev1.Pod{{
			ObjectMeta: metav1.ObjectMeta{Name: "remedik-node-drain-abcde-pod"},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 2}},
			}}},
		}},
	}
	a := NewJobRun(c, &stubLogs{output: "permission denied"}, "remedik", nil)
	a.poll = time.Millisecond

	executed := action.Result{Outputs: map[string]string{"job": "remedik-node-drain-abcde"}}
	result, err := a.Verify(context.Background(), jobRequest(nil), executed)
	if err == nil {
		t.Fatal("Verify() error = nil; a Job that failed is not a remediation that happened")
	}
	if result.Outputs["exitCode"] != "2" {
		t.Errorf("outputs = %v, want the exit code", result.Outputs)
	}
	if !strings.Contains(result.Outputs["logs"], "permission denied") {
		t.Errorf("outputs = %v, want what the script said before it stopped", result.Outputs)
	}
}

// Without the JobFailed condition, the first failed pod of a Job with a
// backoff limit would read as a failed remediation while the retry was still
// to come.
func TestJobRun_AFailedPodIsNotYetAFailedJob(t *testing.T) {
	c := &jobClient{states: []*batchv1.Job{jobAt("remedik-node-drain-abcde", 0, 1, false)}}
	a := NewJobRun(c, &stubLogs{}, "remedik", nil)
	a.poll = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	executed := action.Result{Outputs: map[string]string{"job": "remedik-node-drain-abcde"}}
	result, err := a.Verify(ctx, jobRequest(nil), executed)
	if err == nil {
		t.Fatal("Verify() error = nil; the Job never finished")
	}
	if result.Outputs["jobOutcome"] != "Running" {
		t.Errorf("outputs = %v, want it still Running rather than Failed", result.Outputs)
	}
}

// --------------------------------------------------------------------------
// script.run
// --------------------------------------------------------------------------

func TestScriptRun_MountsTheScriptAndRunsIt(t *testing.T) {
	c := &jobClient{configMap: &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorNamespace, Name: "runbooks"},
		Data:       map[string]string{"drain.sh": "#!/bin/sh\necho draining\n"},
	}}
	a := NewScriptRun(c, &stubLogs{}, "remedik", func() time.Time { return fixedClock })

	result, err := a.Execute(context.Background(), jobRequest(action.Params{
		ImageParam:          "busybox:1.37",
		ConfigMapParam:      "runbooks",
		ServiceAccountParam: "runbook-runner",
	}))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	pod := c.created.Spec.Template.Spec
	if len(pod.Volumes) != 1 || pod.Volumes[0].ConfigMap.Name != "runbooks" {
		t.Fatalf("volumes = %+v, want the ConfigMap mounted", pod.Volumes)
	}
	if got := pod.Containers[0].Command; len(got) != 2 || !strings.HasSuffix(got[1], "drain.sh") {
		t.Errorf("command = %v, want the mounted script", got)
	}
	// With one key, the step does not have to name it.
	if result.Outputs["script"] != "runbooks/drain.sh" {
		t.Errorf("outputs = %v, want the script that will run", result.Outputs)
	}
}

func TestScriptRun_RefusesWhatItCannotFind(t *testing.T) {
	twoKeys := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorNamespace, Name: "runbooks"},
		Data:       map[string]string{"drain.sh": "", "restart.sh": ""},
	}

	tests := []struct {
		name      string
		configMap *corev1.ConfigMap
		params    action.Params
		wantErr   string
	}{
		{
			name:    "no configMap named",
			params:  action.Params{ImageParam: "busybox"},
			wantErr: "no configMap",
		},
		{
			name:    "a ConfigMap that does not exist",
			params:  action.Params{ImageParam: "busybox", ConfigMapParam: "absent"},
			wantErr: "does not exist",
		},
		{
			// Picking one of several silently would be choosing which script
			// runs on somebody's behalf.
			name:      "several keys and no choice",
			configMap: twoKeys,
			params:    action.Params{ImageParam: "busybox", ConfigMapParam: "runbooks"},
			wantErr:   "so the step must say which",
		},
		{
			name:      "a key that is not there",
			configMap: twoKeys,
			params:    action.Params{ImageParam: "busybox", ConfigMapParam: "runbooks", ConfigMapKeyParam: "absent.sh"},
			wantErr:   `has no "absent.sh" key`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &jobClient{configMap: tc.configMap}
			a := NewScriptRun(c, &stubLogs{}, "remedik", nil)

			if _, err := a.Execute(context.Background(), jobRequest(tc.params)); err == nil ||
				!strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
			if c.created != nil {
				t.Error("a Job was created despite the refusal")
			}
		})
	}
}

func TestJobRunNames(t *testing.T) {
	c := &jobClient{}
	if got := NewJobRun(c, nil, "", nil).Name(); got != "job.run" {
		t.Errorf("Name() = %q, want job.run", got)
	}
	if got := NewScriptRun(c, nil, "", nil).Name(); got != "script.run" {
		t.Errorf("Name() = %q, want script.run", got)
	}
}

func TestSanitiseName(t *testing.T) {
	tests := map[string]string{
		"pod-crashloop":         "pod-crashloop",
		"Pod CrashLoop!":        "pod-crashloop",
		"":                      "remediation",
		strings.Repeat("a", 60): strings.Repeat("a", 40),
	}
	for in, want := range tests {
		if got := sanitiseName(in); got != want {
			t.Errorf("sanitiseName(%q) = %q, want %q", in, got, want)
		}
	}
}
