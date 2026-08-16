package metrics

import (
	"sort"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/remedik/remedik/internal/engine"
	"github.com/remedik/remedik/internal/gateway"
)

// The adapters must satisfy the interfaces they are written for; a
// signature drift would otherwise only surface when wiring the operator.
func TestAdaptersImplementTheirInterfaces(_ *testing.T) {
	var _ gateway.Recorder = Gateway{}
	var _ engine.Recorder = Engine{}
}

// gather registers every metric in a private registry and collects it, so
// the test never depends on global state or on registration order.
func gather(t *testing.T) map[string]float64 {
	t.Helper()

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(
		alertsReceived, alertsTruncated, alertsUnmatched, ingestErrors,
		unauthorized, guardRejections, remediationsStarted,
		remediationsFinished, remediationDuration,
	)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	out := map[string]float64{}
	for _, family := range families {
		var total float64
		for _, metric := range family.GetMetric() {
			switch {
			case metric.GetCounter() != nil:
				total += metric.GetCounter().GetValue()
			case metric.GetHistogram() != nil:
				total += float64(metric.GetHistogram().GetSampleCount())
			}
		}
		out[family.GetName()] = total
	}
	return out
}

func TestMetricsRecordValues(t *testing.T) {
	Gateway{}.AlertsReceived(3)
	Gateway{}.AlertsTruncated(1)
	Gateway{}.IngestError("malformed_payload")
	Gateway{}.Unauthorized()
	Engine{}.Unmatched()
	Engine{}.GuardRejected("restart-api", "cooldown")
	Engine{}.RemediationStarted("restart-api")
	Engine{}.RemediationFinished("restart-api", "Succeeded", 2.5)

	got := gather(t)

	want := map[string]float64{
		"remedik_alerts_received_total":        3,
		"remedik_alerts_truncated_total":       1,
		"remedik_alerts_unmatched_total":       1,
		"remedik_ingest_errors_total":          1,
		"remedik_unauthorized_total":           1,
		"remedik_guard_rejections_total":       1,
		"remedik_remediations_started_total":   1,
		"remedik_remediations_total":           1,
		"remedik_remediation_duration_seconds": 1,
	}
	for name, wantValue := range want {
		if got[name] != wantValue {
			t.Errorf("%s = %v, want %v", name, got[name], wantValue)
		}
	}
}

// Metric names are a public interface: dashboards and alerts are written
// against them, so renaming one must be a deliberate, visible change.
func TestMetricNamesAreStable(t *testing.T) {
	want := []string{
		"remedik_alerts_received_total",
		"remedik_alerts_truncated_total",
		"remedik_alerts_unmatched_total",
		"remedik_guard_rejections_total",
		"remedik_ingest_errors_total",
		"remedik_remediation_duration_seconds",
		"remedik_remediations_started_total",
		"remedik_remediations_total",
		"remedik_unauthorized_total",
	}

	got := make([]string, 0, len(want))
	for name := range gather(t) {
		got = append(got, name)
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("exposed metrics = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("metric[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
