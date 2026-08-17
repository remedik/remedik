package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/action"
	"github.com/remedik/remedik/internal/alert"
	"github.com/remedik/remedik/internal/guards"
)

// configMapReader serves one ConfigMap, or an error.
type configMapReader struct {
	client.Reader

	cm  *corev1.ConfigMap
	err error
	// gets counts reads, so a test can prove the last known state is kept
	// rather than re-read.
	gets int
}

func (r *configMapReader) Get(
	_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption,
) error {
	r.gets++
	if r.err != nil {
		return r.err
	}
	if r.cm == nil {
		return apierrors.NewNotFound(
			schema.GroupResource{Resource: "configmaps"}, key.Name)
	}
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return errors.New("not a ConfigMap")
	}
	*cm = *r.cm.DeepCopy()
	return nil
}

func pauseConfigMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "remedik-pause"},
		Data:       data,
	}
}

func newWatcher(t *testing.T, r *configMapReader) (*PauseWatcher, *Pause) {
	t.Helper()
	p := &Pause{}
	return &PauseWatcher{
		Reader:    r,
		Namespace: testNamespace,
		Name:      "remedik-pause",
		Pause:     p,
		Logger:    quietLogger(),
	}, p
}

func TestPause_ReadsTheSwitch(t *testing.T) {
	// Typed during an incident, so every spelling anybody would reach for
	// works. Anything unrecognised means not paused: a switch that stopped
	// remediation because of a typo is worse than one that ignored it.
	for value, want := range map[string]bool{
		"true": true, "TRUE": true, "yes": true, "on": true, "1": true,
		"false": false, "no": false, "off": false, "": false,
		"maybe": false, "tru": false,
	} {
		r := &configMapReader{cm: pauseConfigMap(map[string]string{PauseKey: value})}
		w, p := newWatcher(t, r)
		w.poll(context.Background())
		if p.Paused() != want {
			t.Errorf("paused=%q read as %v, want %v", value, p.Paused(), want)
		}
	}
}

func TestPause_MissingConfigMapMeansRunning(t *testing.T) {
	w, p := newWatcher(t, &configMapReader{})
	w.poll(context.Background())

	if p.Paused() {
		t.Error("paused with no ConfigMap; absence must mean running, since the " +
			"on position stops the product working")
	}
}

// A read failure must change nothing. Flipping to paused because the API server
// hiccuped would be a self-inflicted outage of remediation; flipping to running
// would ignore somebody's deliberate stop.
func TestPause_AReadFailureKeepsTheLastKnownState(t *testing.T) {
	r := &configMapReader{cm: pauseConfigMap(map[string]string{PauseKey: "true"})}
	w, p := newWatcher(t, r)
	w.poll(context.Background())
	if !p.Paused() {
		t.Fatal("expected paused after reading the switch")
	}

	r.err = errors.New("the api server is having a moment")
	w.poll(context.Background())

	if !p.Paused() {
		t.Error("a failed read resumed remediation, ignoring a deliberate stop")
	}
}

func TestPause_CarriesTheReason(t *testing.T) {
	r := &configMapReader{cm: pauseConfigMap(map[string]string{
		PauseKey:       "true",
		PauseReasonKey: "network incident, nothing is trustworthy",
	})}
	w, p := newWatcher(t, r)
	w.poll(context.Background())

	if got := p.Reason(); got != "network incident, nothing is trustworthy" {
		t.Errorf("Reason() = %q, want the note from the ConfigMap", got)
	}
}

// Every replica must know it is paused, not only the leader: the gateway
// answers on all of them.
func TestPause_EveryReplicaWatchesIt(t *testing.T) {
	w := &PauseWatcher{}
	if w.NeedLeaderElection() {
		t.Error("NeedLeaderElection() = true; a standby that took over believing " +
			"remediation was enabled would act on the first alert it saw")
	}
}

// A nil Pause is never paused, so the field stays optional.
func TestPause_NilIsNeverPaused(t *testing.T) {
	var p *Pause
	if p.Paused() {
		t.Error("a nil Pause reported paused")
	}
	if p.Reason() != "" {
		t.Error("a nil Pause reported a reason")
	}
}

// The behaviour that matters: paused does not silence remedik, it forces
// dry-run. The one time an operator most wants to know what remediation would
// have done is the moment they stopped it.
func TestSink_PausedRecordsWhatItWouldHaveDone(t *testing.T) {
	strategy := strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"})

	registry, err := action.NewRegistry(&scriptedAction{name: "deployment.restart"})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	pause := &Pause{}
	pause.set(true, "network incident")

	c := newFakeClient(strategy)
	metrics := newCountingRecorder()
	sink := &Sink{
		Client: c, Registry: registry, History: guards.NewMemoryHistory(0),
		Namespace: testNamespace,
		// Live posture: the pause has to override it, which is the point.
		Posture: NewPosture(false, nil),
		Metrics: metrics, Logger: quietLogger(),
		Now:   func() time.Time { return testClock },
		Pause: pause,
	}

	sink.Consume([]alert.Alert{firingAlert()})

	records := c.remediations()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: pausing must not lose the record", len(records))
	}
	rem := records[0]

	if !rem.Spec.DryRun {
		t.Error("DryRun = false while paused; the posture must be overridden")
	}
	if rem.Labels[v1alpha1.LabelPaused] != "true" {
		t.Error("the record does not say it was made while paused, so a run of " +
			"simulations is indistinguishable from a dry-run trial")
	}
	if got := rem.Annotations[v1alpha1.AnnotationPauseReason]; got != "network incident" {
		t.Errorf("pause reason on the record = %q, want the note", got)
	}
}

// And unpaused, the posture decides as before.
func TestSink_NotPausedLeavesThePostureAlone(t *testing.T) {
	strategy := strategy("restart-api", map[string]string{"alertname": "KubePodCrashLooping"})

	registry, err := action.NewRegistry(&scriptedAction{name: "deployment.restart"})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	c := newFakeClient(strategy)
	sink := &Sink{
		Client: c, Registry: registry, History: guards.NewMemoryHistory(0),
		Namespace: testNamespace, Posture: NewPosture(false, nil),
		Metrics: newCountingRecorder(), Logger: quietLogger(),
		Now:   func() time.Time { return testClock },
		Pause: &Pause{},
	}

	sink.Consume([]alert.Alert{firingAlert()})

	rem := c.remediations()[0]
	if rem.Spec.DryRun {
		t.Error("DryRun = true with a live posture and no pause")
	}
	if _, marked := rem.Labels[v1alpha1.LabelPaused]; marked {
		t.Error("an unpaused record carries the paused marker")
	}
}
