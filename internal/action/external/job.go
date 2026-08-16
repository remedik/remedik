package external

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/internal/action"
)

// Parameters shared by job.run and script.run.
const (
	// ImageParam is the container image to run. Required.
	ImageParam = "image"
	// CommandParam is a JSON array — ["/bin/sh","-c","..."] — rather than a
	// string. There are no quoting rules to invent and no word-splitting to
	// surprise anyone at 3am.
	CommandParam = "command"
	// ServiceAccountParam names the ServiceAccount the Job runs as. It
	// defaults to "default", never to remedik's own: a strategy author must
	// not inherit the operator's permissions by omission.
	ServiceAccountParam = "serviceAccount"
	// TTLParam is how long the finished Job is kept, so its logs survive
	// long enough to be read.
	TTLParam = "ttlSecondsAfterFinished"
	// BackoffParam is how many times Kubernetes retries the Job's pod.
	BackoffParam = "backoffLimit"
	// ConfigMapParam and ConfigMapKeyParam name the script for script.run.
	ConfigMapParam    = "configMap"
	ConfigMapKeyParam = "key"
)

// DefaultTTL keeps a finished Job for an hour: long enough that somebody
// reading the Remediation record can still reach the pod, short enough that
// remediation does not accumulate Jobs.
const DefaultTTL int32 = 3600

// logTailLines is how much of the Job's output is recorded. The record lives
// in etcd, which is not a log store; the point is the last thing the script
// said before it stopped.
const logTailLines int64 = 20

// maxLogBytes bounds that further, for a script that prints one very long
// line.
const maxLogBytes = 2 << 10

// envSafe matches the characters allowed in an environment variable name.
var envSafe = regexp.MustCompile(`[^A-Z0-9_]`)

// JobRun runs somebody else's container as a remediation step.
//
// This is the escape hatch, and its shape is all about bounding it. The Job
// is created in remedik's own namespace, so the permission stays namespaced
// and a strategy cannot reach into another team's. It runs under a
// ServiceAccount the step names, never remedik's, so authority is granted
// deliberately rather than inherited. And the command is a JSON array, so
// there is no shell between what was written and what runs.
type JobRun struct {
	client client.Client
	// pods reads container logs. The controller-runtime client cannot: logs
	// are a subresource it does not model, and the tail of what a script
	// printed before it stopped is most of this action's value.
	pods logReader
	name string
	now  func() time.Time
	poll time.Duration

	// operatorAccount is remedik's own ServiceAccount, refused explicitly so
	// that a strategy author cannot inherit every permission the operator
	// holds by writing one word.
	operatorAccount string

	// script makes this instance script.run: the command is taken from a
	// ConfigMap mounted into the container instead of from the image.
	script bool
}

// logReader is the slice of a Kubernetes clientset this action needs. An
// interface rather than the clientset itself, so a test can serve output
// without a cluster.
type logReader interface {
	TailLogs(ctx context.Context, namespace, pod string, lines int64) (string, error)
}

// DefaultJobPoll is how often verification re-reads the Job.
const DefaultJobPoll = 2 * time.Second

// NewJobRun builds the action that runs an image.
func NewJobRun(c client.Client, pods logReader, operatorAccount string, now func() time.Time) *JobRun {
	return &JobRun{
		client: c, pods: pods, name: "job.run",
		now: orNow(now), poll: DefaultJobPoll, operatorAccount: operatorAccount,
	}
}

// NewScriptRun builds the action that runs a script from a ConfigMap, so a
// runbook can be edited without rebuilding an image.
func NewScriptRun(c client.Client, pods logReader, operatorAccount string, now func() time.Time) *JobRun {
	return &JobRun{
		client: c, pods: pods, name: "script.run",
		now: orNow(now), poll: DefaultJobPoll, operatorAccount: operatorAccount,
		script: true,
	}
}

func orNow(now func() time.Time) func() time.Time {
	if now == nil {
		return time.Now
	}
	return now
}

// Name implements action.Action.
func (a *JobRun) Name() string { return a.name }

// Resolve returns no target: the Job is the work, and it acts on whatever
// the script decides.
func (a *JobRun) Resolve(map[string]string, action.Params) (action.Target, error) {
	return action.Target{}, nil
}

// Plan describes the Job, validating everything Execute needs — including
// that the ConfigMap exists — so a dry run cannot promise a Job that would
// fail to create.
func (a *JobRun) Plan(ctx context.Context, req action.Request) (action.Result, error) {
	spec, err := a.prepare(ctx, req)
	if err != nil {
		return action.Result{}, err
	}

	result := action.Result{
		Summary: fmt.Sprintf("run %s in namespace %s as serviceaccount/%s: %s",
			spec.image, req.Namespace, spec.serviceAccount, strings.Join(spec.command, " ")),
		Kubectl: spec.kubectl(req.Namespace),
	}
	result.Output("image", spec.image)
	result.Output("serviceAccount", spec.serviceAccount)
	if spec.configMap != "" {
		result.Output("script", spec.configMap+"/"+spec.configMapKey)
	}
	return result, nil
}

// Execute creates the Job. It does not wait: verification does that, bounded
// by the step's verifyTimeout, so a long-running script cannot hold the
// reconcile worker for longer than the strategy said it may.
func (a *JobRun) Execute(ctx context.Context, req action.Request) (action.Result, error) {
	spec, err := a.prepare(ctx, req)
	if err != nil {
		return action.Result{}, err
	}

	job := spec.build(req, a.now())
	if err := a.client.Create(ctx, job); err != nil {
		if apierrors.IsForbidden(err) {
			return action.Result{}, fmt.Errorf(
				"not permitted to create a Job in %s: %w", req.Namespace, err)
		}
		return action.Result{}, fmt.Errorf("create the Job: %w", err)
	}

	result := action.Result{
		Summary: fmt.Sprintf("created job/%s running %s as serviceaccount/%s",
			job.Name, spec.image, spec.serviceAccount),
		Kubectl: spec.kubectl(req.Namespace),
	}
	result.Output("job", job.Name)
	result.Output("image", spec.image)
	result.Output("serviceAccount", spec.serviceAccount)
	if spec.configMap != "" {
		result.Output("script", spec.configMap+"/"+spec.configMapKey)
	}
	return result, nil
}

// Verify waits for the Job and records what it said.
//
// A Job that was created is not a remediation that happened. What an
// operator needs on the record is the exit status and the last thing the
// script printed before it stopped — which is the whole reason this action
// is worth more than "kubectl create job".
func (a *JobRun) Verify(
	ctx context.Context, req action.Request, executed action.Result,
) (action.Result, error) {
	ctx, cancel := action.WithVerifyDeadline(ctx)
	defer cancel()

	name := executed.Outputs["job"]
	if name == "" {
		return action.Result{}, fmt.Errorf("no Job was recorded to verify")
	}

	for {
		var job batchv1.Job
		key := client.ObjectKey{Namespace: req.Namespace, Name: name}
		if err := a.client.Get(ctx, key, &job); err != nil {
			return action.Result{}, fmt.Errorf("read job/%s: %w", name, err)
		}

		switch {
		case job.Status.Succeeded > 0:
			result := action.Result{Summary: fmt.Sprintf("job/%s succeeded", name)}
			result.Output("jobOutcome", "Succeeded")
			a.attachLogs(ctx, req.Namespace, name, &result)
			return result, nil

		case job.Status.Failed > 0 && failedForGood(&job):
			result := action.Result{Summary: fmt.Sprintf("job/%s failed", name)}
			result.Output("jobOutcome", "Failed")
			a.attachLogs(ctx, req.Namespace, name, &result)
			return result, fmt.Errorf("job/%s failed after %d attempt(s)", name, job.Status.Failed)
		}

		says := fmt.Sprintf("job/%s is still running", name)
		select {
		case <-ctx.Done():
			result := action.Result{Summary: says}
			result.Output("jobOutcome", "Running")
			a.attachLogs(ctx, req.Namespace, name, &result)
			return result, fmt.Errorf("%s and did not finish in time", says)
		case <-time.After(a.poll):
		}
	}
}

// failedForGood reports whether Kubernetes has stopped retrying the Job.
// Without this, the first failed pod of a Job with a backoff limit would be
// read as a failed remediation while the retry was still to come.
func failedForGood(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// attachLogs records the tail of the Job's output. Failing to read it is not
// a failure of the remediation: the Job's outcome is already known, and a
// missing log is worth less than a step that reports nothing at all.
func (a *JobRun) attachLogs(ctx context.Context, namespace, job string, result *action.Result) {
	var pods corev1.PodList
	if err := a.client.List(ctx, &pods,
		client.InNamespace(namespace),
		client.MatchingLabels{"job-name": job}); err != nil {
		result.Output("logs", "could not list the Job's pods: "+err.Error())
		return
	}
	if len(pods.Items) == 0 {
		result.Output("logs", "the Job left no pod to read")
		return
	}

	// The newest pod is the attempt that decided the outcome.
	newest := &pods.Items[0]
	for i := range pods.Items {
		if pods.Items[i].CreationTimestamp.After(newest.CreationTimestamp.Time) {
			newest = &pods.Items[i]
		}
	}

	if code, ok := exitCode(newest); ok {
		result.Output("exitCode", strconv.Itoa(int(code)))
	}
	result.Output("pod", newest.Name)

	if a.pods == nil {
		return
	}
	logs, err := a.pods.TailLogs(ctx, namespace, newest.Name, logTailLines)
	if err != nil {
		result.Output("logs", "could not read the pod's logs: "+err.Error())
		return
	}
	if logs = strings.TrimSpace(logs); logs != "" {
		result.Output("logs", capLog(logs))
	}
}

// capLog bounds what is written to the record. It lives in etcd, which is
// not a log store; the Job is kept for an hour so the rest is one kubectl
// away.
func capLog(s string) string {
	if len(s) <= maxLogBytes {
		return s
	}
	return "…" + s[len(s)-maxLogBytes:]
}

func exitCode(pod *corev1.Pod) (int32, bool) {
	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Terminated != nil {
			return status.State.Terminated.ExitCode, true
		}
	}
	return 0, false
}

// jobSpec is a validated Job, built identically by Plan and Execute.
type jobSpec struct {
	image          string
	command        []string
	serviceAccount string
	ttl            int32
	backoff        int32
	configMap      string
	configMapKey   string
}

const scriptMountPath = "/remedik/scripts"

func (s jobSpec) kubectl(namespace string) string {
	if s.configMap != "" {
		return fmt.Sprintf(
			"kubectl create job remedik-<name> -n %s --image=%s -- %s  # with %s/%s mounted at %s",
			namespace, s.image, strings.Join(s.command, " "),
			s.configMap, s.configMapKey, scriptMountPath)
	}
	return fmt.Sprintf("kubectl create job remedik-<name> -n %s --image=%s -- %s",
		namespace, s.image, strings.Join(s.command, " "))
}

// build assembles the Job.
func (s jobSpec) build(req action.Request, now time.Time) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			// GenerateName rather than a name derived from the remediation:
			// a retried step must not collide with the Job its previous
			// attempt left behind.
			GenerateName: "remedik-" + sanitiseName(req.Strategy) + "-",
			Namespace:    req.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "remedik",
				"remedik.dev/strategy":         sanitiseName(req.Strategy),
			},
			Annotations: map[string]string{
				"remedik.dev/remediation": req.Remediation,
				"remedik.dev/created-at":  now.UTC().Format(time.RFC3339),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &s.backoff,
			TTLSecondsAfterFinished: &s.ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: s.serviceAccount,
					Containers: []corev1.Container{{
						Name:    "remediation",
						Image:   s.image,
						Command: s.command,
						Env:     environment(req),
					}},
				},
			},
		},
	}

	if s.configMap != "" {
		const volumeName = "script"
		job.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: s.configMap},
					DefaultMode:          ptr(int32(0o555)),
				},
			},
		}}
		job.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
			Name:      volumeName,
			MountPath: scriptMountPath,
			ReadOnly:  true,
		}}
	}

	return job
}

// environment hands the incident to the container.
//
// Alert labels arrive as REMEDIK_ALERT_<LABEL>. The prefix exists so that a
// label called PATH cannot replace the container's, which is the kind of
// thing that happens once and is never diagnosed.
func environment(req action.Request) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "REMEDIK_REMEDIATION", Value: req.Remediation},
		{Name: "REMEDIK_STRATEGY", Value: req.Strategy},
		{Name: "REMEDIK_NAMESPACE", Value: req.Namespace},
		{Name: "REMEDIK_TARGET", Value: targetOrEmpty(req.Target)},
	}

	labels, _ := json.Marshal(req.Labels)
	env = append(env, corev1.EnvVar{Name: "REMEDIK_ALERT_LABELS", Value: string(labels)})

	for _, key := range sortedKeys(req.Labels) {
		name := envSafe.ReplaceAllString(strings.ToUpper(key), "_")
		env = append(env, corev1.EnvVar{Name: "REMEDIK_ALERT_" + name, Value: req.Labels[key]})
	}
	return env
}

func (a *JobRun) prepare(ctx context.Context, req action.Request) (jobSpec, error) {
	spec := jobSpec{
		image:          req.Params.Get(ImageParam, ""),
		serviceAccount: req.Params.Get(ServiceAccountParam, "default"),
		ttl:            DefaultTTL,
		backoff:        0,
	}

	if spec.image == "" {
		return jobSpec{}, fmt.Errorf("no %s: the step must name the image to run", ImageParam)
	}

	command, err := parseCommand(req.Params.Get(CommandParam, ""))
	if err != nil {
		return jobSpec{}, err
	}

	if a.script {
		if err := a.prepareScript(ctx, req, &spec, &command); err != nil {
			return jobSpec{}, err
		}
	}
	if len(command) == 0 {
		return jobSpec{}, fmt.Errorf("no %s: the step must say what to run, as a JSON array such as "+
			`["/bin/sh","-c","echo hello"]`, CommandParam)
	}
	spec.command = command

	if raw := req.Params.Get(TTLParam, ""); raw != "" {
		ttl, err := parseInt32(raw, TTLParam)
		if err != nil {
			return jobSpec{}, err
		}
		spec.ttl = ttl
	}
	if raw := req.Params.Get(BackoffParam, ""); raw != "" {
		backoff, err := parseInt32(raw, BackoffParam)
		if err != nil {
			return jobSpec{}, err
		}
		spec.backoff = backoff
	}

	// remedik's own ServiceAccount is refused explicitly, because the
	// alternative is a strategy author inheriting every permission the
	// operator holds by writing one word.
	if a.operatorAccount != "" && spec.serviceAccount == a.operatorAccount {
		return jobSpec{}, fmt.Errorf(
			"a Job may not run as %q, which is remedik's own ServiceAccount: it would inherit "+
				"every permission the operator holds. Name a ServiceAccount with exactly the "+
				"access this remediation needs", a.operatorAccount)
	}

	return spec, nil
}

// prepareScript resolves the ConfigMap and points the command at the file.
func (a *JobRun) prepareScript(
	ctx context.Context, req action.Request, spec *jobSpec, command *[]string,
) error {
	spec.configMap = req.Params.Get(ConfigMapParam, "")
	if spec.configMap == "" {
		return fmt.Errorf("no %s: the step must name the ConfigMap holding the script", ConfigMapParam)
	}

	var cm corev1.ConfigMap
	// From remedik's own namespace, and only there. Reading a script from
	// the namespace an alert names would mean anyone with write access
	// anywhere in the cluster could get code executed by the operator: a
	// privilege escalation dressed up as a feature.
	key := client.ObjectKey{Namespace: req.Namespace, Name: spec.configMap}
	if err := a.client.Get(ctx, key, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("configmap %s/%s does not exist", req.Namespace, spec.configMap)
		}
		return fmt.Errorf("read configmap %s/%s: %w", req.Namespace, spec.configMap, err)
	}

	spec.configMapKey = req.Params.Get(ConfigMapKeyParam, "")
	if spec.configMapKey == "" {
		if len(cm.Data) != 1 {
			return fmt.Errorf("no %s: configmap %s/%s holds %d keys, so the step must say which",
				ConfigMapKeyParam, req.Namespace, spec.configMap, len(cm.Data))
		}
		for only := range cm.Data {
			spec.configMapKey = only
		}
	}
	if _, ok := cm.Data[spec.configMapKey]; !ok {
		return fmt.Errorf("configmap %s/%s has no %q key",
			req.Namespace, spec.configMap, spec.configMapKey)
	}

	if len(*command) == 0 {
		*command = []string{"/bin/sh", scriptMountPath + "/" + spec.configMapKey}
	}
	return nil
}

// parseCommand reads the command as JSON.
//
// A JSON array rather than a string, because a string needs quoting rules,
// and quoting rules invented for a YAML field are how a remediation ends up
// running something nobody wrote.
func parseCommand(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var command []string
	if err := json.Unmarshal([]byte(raw), &command); err != nil {
		return nil, fmt.Errorf("parameter %q: %w — it is a JSON array, such as "+
			`["/bin/sh","-c","echo hello"]`, CommandParam, err)
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("parameter %q: the array is empty", CommandParam)
	}
	return command, nil
}

func parseInt32(raw, param string) (int32, error) {
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parameter %q: %q is not a whole number", param, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("parameter %q: %d is negative", param, n)
	}
	return int32(n), nil
}

// sanitiseName makes a strategy name safe to put in an object name.
var nameUnsafe = regexp.MustCompile(`[^a-z0-9-]`)

func sanitiseName(s string) string {
	out := nameUnsafe.ReplaceAllString(strings.ToLower(s), "-")
	out = strings.Trim(out, "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	if out == "" {
		out = "remediation"
	}
	return out
}

func ptr[T any](v T) *T { return &v }

// sortedKeys makes the environment block deterministic, so two runs of the
// same remediation produce the same Job rather than a diff nobody ordered.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Compile-time proof that the action satisfies the contract.
var (
	_ action.Action   = (*JobRun)(nil)
	_ action.Verifier = (*JobRun)(nil)
)
