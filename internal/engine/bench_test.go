package engine

import (
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
	"github.com/remedik/remedik/internal/action"
	"github.com/remedik/remedik/internal/alert"
	"github.com/remedik/remedik/internal/guards"
)

// The sink's hot path is one Alertmanager delivery, and a delivery is a group:
// Alertmanager batches by `group_by` and sends what accumulated during
// `group_wait`, so during the storm remedik exists to absorb a single POST can
// carry hundreds of alerts.
//
// That makes per-delivery cost the number that matters, not per-alert.
//
//	go test ./internal/engine/ -run XXX -bench Sink -benchmem

const (
	benchStrategies = 40
	benchAlerts     = 200
)

func benchStrategyObjects(n int) []client.Object {
	enabled := true
	out := make([]client.Object, 0, n)
	for i := range n {
		out = append(out, &v1alpha1.RemediationStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("strategy-%02d", i)},
			Spec: v1alpha1.RemediationStrategySpec{
				Enabled: &enabled,
				Trigger: v1alpha1.Trigger{
					Match: map[string]string{"alertname": fmt.Sprintf("Alert%02d", i)},
				},
				Steps: []v1alpha1.Step{{Action: "deployment.restart"}},
			},
		})
	}
	return out
}

func benchAlertBatch(n int) []alert.Alert {
	out := make([]alert.Alert, 0, n)
	for i := range n {
		out = append(out, alert.Alert{
			Status:      "firing",
			Fingerprint: fmt.Sprintf("fp-%04d", i),
			Labels: map[string]string{
				// Half match a strategy and half do not, which is what a real
				// delivery looks like: remedik is routed more than it acts on.
				"alertname":  fmt.Sprintf("Alert%02d", i%(benchStrategies*2)),
				"namespace":  "payments",
				"deployment": "api",
			},
			StartsAt: testClock,
		})
	}
	return out
}

func benchSink(b *testing.B) *Sink {
	b.Helper()

	registry, err := action.NewRegistry(&scriptedAction{name: "deployment.restart"})
	if err != nil {
		b.Fatalf("NewRegistry() error = %v", err)
	}
	return &Sink{
		Client:    newFakeClient(benchStrategyObjects(benchStrategies)...),
		Registry:  registry,
		History:   guards.NewMemoryHistory(0),
		Namespace: testNamespace,
		Posture:   NewPosture(true, nil),
		Metrics:   newCountingRecorder(),
		Logger:    quietLogger(),
		Now:       func() time.Time { return testClock },
	}
}

func BenchmarkSinkOneDelivery(b *testing.B) {
	sink := benchSink(b)
	alerts := benchAlertBatch(benchAlerts)

	b.ReportAllocs()
	for b.Loop() {
		sink.Consume(alerts)
	}
}

// A single alert, for comparison: the difference between this multiplied by
// two hundred and the benchmark above is the work that should not be repeated.
func BenchmarkSinkOneAlert(b *testing.B) {
	sink := benchSink(b)
	alerts := benchAlertBatch(1)

	b.ReportAllocs()
	for b.Loop() {
		sink.Consume(alerts)
	}
}
