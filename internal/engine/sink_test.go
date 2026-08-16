package engine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/action"
	"github.com/remedik/remedik/internal/alert"
	"github.com/remedik/remedik/internal/guards"
)

func strategy(name string, match map[string]string, mutate ...func(*v1alpha1.RemediationStrategy)) *v1alpha1.RemediationStrategy {
	s := &v1alpha1.RemediationStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.RemediationStrategySpec{
			Trigger: v1alpha1.Trigger{Match: match},
			Steps:   []v1alpha1.Step{{Action: "deployment.restart"}},
		},
	}
	for _, m := range mutate {
		m(s)
	}
	return s
}

func firingAlert() alert.Alert {
	return alert.Alert{
		Fingerprint: "f1",
		Status:      alert.StatusFiring,
		StartsAt:    testClock.Add(-5 * time.Minute),
		Labels: map[string]string{
			"alertname":  "KubePodCrashLooping",
			"namespace":  "payments",
			"deployment": "api",
		},
	}
}

type sinkFixture struct {
	sink    *Sink
	client  *fakeClient
	metrics *countingRecorder
	history *guards.MemoryHistory
}

func newSink(t *testing.T, dryRun bool, objs ...client.Object) *sinkFixture {
	t.Helper()

	registry, err := action.NewRegistry(&scriptedAction{name: "deployment.restart"})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	c := newFakeClient(objs...)
	metrics := newCountingRecorder()
	history := guards.NewMemoryHistory(0)

	return &sinkFixture{
		client:  c,
		metrics: metrics,
		history: history,
		sink: &Sink{
			Client:    c,
			Registry:  registry,
			History:   history,
			Namespace: testNamespace,
			Posture:   NewPosture(dryRun, nil),
			Metrics:   metrics,
			Logger:    quietLogger(),
			Now:       func() time.Time { return testClock },
		},
	}
}

func TestSink_CreatesRemediationForMatchingAlert(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"},
		func(s *v1alpha1.RemediationStrategy) { s.Spec.OnFailure.Retries = 2 }))

	f.sink.Consume([]alert.Alert{firingAlert()})

	created := f.client.remediations()
	if len(created) != 1 {
		t.Fatalf("created %d remediations, want 1", len(created))
	}
	rem := created[0]

	if rem.Spec.StrategyName != "restart-api" {
		t.Errorf("StrategyName = %q", rem.Spec.StrategyName)
	}
	if rem.Spec.Target != "deployment/payments/api" {
		t.Errorf("Target = %q, want deployment/payments/api", rem.Spec.Target)
	}
	// The sink writes no status: an empty state is what the reconciler
	// reads as "not started", so there is no second write to race with it.
	if rem.Status.State != "" {
		t.Errorf("State = %q, want it left to the reconciler", rem.Status.State)
	}
	// The plan and retry budget are copied so the record survives the
	// strategy being edited or deleted.
	if len(rem.Spec.Steps) != 1 || rem.Spec.Steps[0].Action != "deployment.restart" {
		t.Errorf("Steps = %+v, want the strategy's plan copied", rem.Spec.Steps)
	}
	if rem.Spec.Retries != 2 {
		t.Errorf("Retries = %d, want the strategy's budget copied", rem.Spec.Retries)
	}
	if rem.Spec.Alert.Fingerprint != "f1" || rem.Spec.Alert.Name != "KubePodCrashLooping" {
		t.Errorf("Alert = %+v, want the triggering alert recorded", rem.Spec.Alert)
	}
	if rem.Labels[v1alpha1.LabelStrategy] != "restart-api" {
		t.Errorf("labels = %v, want the strategy label for selectors and pruning", rem.Labels)
	}
	if f.metrics.started != 1 {
		t.Errorf("started metric = %d, want 1", f.metrics.started)
	}
	// The rate guard must see this start.
	if got := f.history.StartsSince("restart-api", testClock.Add(-time.Hour)); got != 1 {
		t.Errorf("history recorded %d starts, want 1", got)
	}
}

func TestSink_DryRunIsRecordedOnTheResource(t *testing.T) {
	f := newSink(t, true, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"}))

	f.sink.Consume([]alert.Alert{firingAlert()})

	created := f.client.remediations()
	if len(created) != 1 {
		t.Fatalf("created %d remediations, want 1", len(created))
	}
	if !created[0].Spec.DryRun {
		t.Error("DryRun = false, want the record to explain its own Simulated outcome")
	}
}

func TestSink_IgnoresResolvedAlerts(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"}))

	resolved := firingAlert()
	resolved.Status = alert.StatusResolved
	f.sink.Consume([]alert.Alert{resolved})

	if got := len(f.client.remediations()); got != 0 {
		t.Errorf("created %d remediations for a resolved alert, want 0", got)
	}
	if f.metrics.unmatched != 0 {
		t.Error("a resolved alert was counted as unmatched")
	}
}

func TestSink_UnmatchedAlertIsCounted(t *testing.T) {
	f := newSink(t, false, strategy("other", map[string]string{"alertname": "KubeNodeNotReady"}))

	f.sink.Consume([]alert.Alert{firingAlert()})

	if got := len(f.client.remediations()); got != 0 {
		t.Errorf("created %d remediations, want 0", got)
	}
	if f.metrics.unmatched != 1 {
		t.Errorf("unmatched metric = %d, want 1", f.metrics.unmatched)
	}
}

func TestSink_DisabledStrategyDoesNotMatch(t *testing.T) {
	disabled := false
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"},
		func(s *v1alpha1.RemediationStrategy) { s.Spec.Enabled = &disabled }))

	f.sink.Consume([]alert.Alert{firingAlert()})

	if got := len(f.client.remediations()); got != 0 {
		t.Errorf("a disabled strategy produced %d remediations", got)
	}
}

func TestSink_CooldownBlocksASecondExecution(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"},
		func(s *v1alpha1.RemediationStrategy) {
			s.Spec.Guards.Cooldown = &metav1.Duration{Duration: 15 * time.Minute}
		}))
	f.history.RecordCompletion("restart-api", "deployment/payments/api", testClock.Add(-5*time.Minute))

	f.sink.Consume([]alert.Alert{firingAlert()})

	if got := len(f.client.remediations()); got != 0 {
		t.Errorf("created %d remediations inside the cooldown, want 0", got)
	}
	if f.metrics.rejected[guards.GuardCooldown] != 1 {
		t.Errorf("rejection metrics = %v, want one cooldown rejection", f.metrics.rejected)
	}
}

func TestSink_RateLimitBlocksAStorm(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"},
		func(s *v1alpha1.RemediationStrategy) { s.Spec.Guards.MaxPerHour = 2 }))
	f.history.RecordStart("restart-api", testClock.Add(-30*time.Minute))
	f.history.RecordStart("restart-api", testClock.Add(-10*time.Minute))

	f.sink.Consume([]alert.Alert{firingAlert()})

	if got := len(f.client.remediations()); got != 0 {
		t.Errorf("created %d remediations over the hourly limit, want 0", got)
	}
	if f.metrics.rejected[guards.GuardMaxPerHour] != 1 {
		t.Errorf("rejection metrics = %v, want one rate rejection", f.metrics.rejected)
	}
}

// An alert that cannot be turned into a target is a misconfiguration. It
// must still leave a record — silence is the one outcome an operator cannot
// debug — and the reconciler is what fails it.
func TestSink_UnresolvableTargetStillLeavesARecord(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"}))

	registry, err := action.NewRegistry(&scriptedAction{
		name:       "deployment.restart",
		resolveErr: errUnresolvable,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	f.sink.Registry = registry

	f.sink.Consume([]alert.Alert{firingAlert()})

	created := f.client.remediations()
	if len(created) != 1 {
		t.Fatalf("created %d remediations, want 1 recording the misconfiguration", len(created))
	}
	if created[0].Spec.Target != "" {
		t.Errorf("Target = %q, want it empty when unresolvable", created[0].Spec.Target)
	}
	if created[0].Spec.StrategyName != "restart-api" {
		t.Errorf("StrategyName = %q, want the record to name the strategy", created[0].Spec.StrategyName)
	}
}

func TestSink_MostSpecificStrategyWins(t *testing.T) {
	broad := strategy("broad", map[string]string{"alertname": "KubePodCrashLooping"})
	specific := strategy("specific", map[string]string{
		"alertname": "KubePodCrashLooping",
		"namespace": "payments",
	})
	f := newSink(t, false, broad, specific)

	f.sink.Consume([]alert.Alert{firingAlert()})

	created := f.client.remediations()
	if len(created) != 1 {
		t.Fatalf("created %d remediations, want exactly 1", len(created))
	}
	if created[0].Spec.StrategyName != "specific" {
		t.Errorf("strategy = %q, want the most specific one", created[0].Spec.StrategyName)
	}
}

func TestSink_EachAlertInABatchIsHandled(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"}))

	first := firingAlert()
	second := firingAlert()
	second.Fingerprint = "f2"
	second.Labels["deployment"] = "web"

	f.sink.Consume([]alert.Alert{first, second})

	if got := len(f.client.remediations()); got != 2 {
		t.Errorf("created %d remediations for a batch of 2, want 2", got)
	}
}

// A failure on one alert must not stop the rest of the batch.
func TestSink_ContinuesAfterAFailedCreate(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"}))
	f.client.createErr = errUnresolvable

	f.sink.Consume([]alert.Alert{firingAlert(), firingAlert()})

	// Nothing was stored, but both alerts were attempted and neither
	// panicked; the guard history must not record starts that never began.
	if got := f.history.StartsSince("restart-api", testClock.Add(-time.Hour)); got != 0 {
		t.Errorf("history recorded %d starts despite failed creates", got)
	}
}

// recordingEvents captures published Kubernetes events.
type recordingEvents struct {
	events []string
}

func (r *recordingEvents) Eventf(
	object runtime.Object, _ runtime.Object, eventtype, reason, _, messageFmt string, args ...any,
) {
	name := "<unknown>"
	if obj, ok := object.(*v1alpha1.RemediationStrategy); ok {
		name = obj.Name
	}
	r.events = append(r.events,
		fmt.Sprintf("%s/%s/%s: %s", name, eventtype, reason, fmt.Sprintf(messageFmt, args...)))
}

// A guard rejection has to be visible where an operator will look for it:
// on the strategy, not only in the operator's logs.
func TestSink_GuardRejectionIsPublishedAsAnEvent(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"},
		func(s *v1alpha1.RemediationStrategy) {
			s.Spec.Guards.Cooldown = &metav1.Duration{Duration: 15 * time.Minute}
		}))
	events := &recordingEvents{}
	f.sink.Events = events
	f.history.RecordCompletion("restart-api", "deployment/payments/api", testClock.Add(-5*time.Minute))

	f.sink.Consume([]alert.Alert{firingAlert()})

	if len(events.events) != 1 {
		t.Fatalf("published %d events, want 1: %v", len(events.events), events.events)
	}
	got := events.events[0]
	for _, want := range []string{
		"restart-api/Normal/" + EventReasonGuardRejected,
		"KubePodCrashLooping",
		`guard "cooldown"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("event = %q, want it to contain %q", got, want)
		}
	}
}

func TestSink_NoEventsWhenNothingIsRejected(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"}))
	events := &recordingEvents{}
	f.sink.Events = events

	f.sink.Consume([]alert.Alert{firingAlert()})

	if len(events.events) != 0 {
		t.Errorf("published %v, want no events for an accepted alert", events.events)
	}
}

// The recorder is optional; a nil one must not panic.
func TestSink_EventRecorderIsOptional(t *testing.T) {
	f := newSink(t, false, strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"},
		func(s *v1alpha1.RemediationStrategy) {
			s.Spec.Guards.Cooldown = &metav1.Duration{Duration: 15 * time.Minute}
		}))
	f.sink.Events = nil
	f.history.RecordCompletion("restart-api", "deployment/payments/api", testClock.Add(-time.Minute))

	f.sink.Consume([]alert.Alert{firingAlert()})
}

// newPostureSink is newSink with an explicit posture rather than a bare
// dry-run flag.
func newPostureSink(t *testing.T, posture Posture, objs ...client.Object) *sinkFixture {
	t.Helper()
	f := newSink(t, true, objs...)
	f.sink.Posture = posture
	return f
}

// The reason per-namespace posture exists: live where remediation has been
// earned, reporting everywhere else, in one install.
func TestSink_ResolvesThePostureFromTheTargetsNamespace(t *testing.T) {
	tests := []struct {
		name       string
		posture    Posture
		namespace  string
		wantDryRun bool
	}{
		{
			name:       "the default applies where nothing overrides it",
			posture:    NewPosture(true, nil),
			namespace:  "payments",
			wantDryRun: true,
		},
		{
			name:       "a live namespace acts although the default simulates",
			posture:    NewPosture(true, map[string]Mode{"payments": ModeLive}),
			namespace:  "payments",
			wantDryRun: false,
		},
		{
			name:       "and its neighbour still simulates",
			posture:    NewPosture(true, map[string]Mode{"staging": ModeLive}),
			namespace:  "payments",
			wantDryRun: true,
		},
		{
			name:       "a held-back namespace reports although the default acts",
			posture:    NewPosture(false, map[string]Mode{"payments": ModeDryRun}),
			namespace:  "payments",
			wantDryRun: true,
		},
		{
			// remedik's own namespace must not decide this. The posture is
			// about the workload being remediated.
			name:       "remedik's own namespace is not the one consulted",
			posture:    NewPosture(true, map[string]Mode{testNamespace: ModeLive}),
			namespace:  "payments",
			wantDryRun: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPostureSink(t, tt.posture,
				strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"}))

			a := firingAlert()
			a.Labels["namespace"] = tt.namespace
			f.sink.Consume([]alert.Alert{a})

			created := f.client.remediations()
			if len(created) != 1 {
				t.Fatalf("created %d remediations, want 1", len(created))
			}
			if got := created[0].Spec.DryRun; got != tt.wantDryRun {
				t.Errorf("Spec.DryRun = %v, want %v (target namespace %q, posture %s)",
					got, tt.wantDryRun, tt.namespace, tt.posture)
			}
		})
	}
}
