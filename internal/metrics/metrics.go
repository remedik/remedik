// Package metrics implements the Prometheus adapters behind the Recorder
// interfaces the gateway and engine define.
//
// Keeping the adapters here means those packages carry no Prometheus
// dependency and stay testable with a counter struct. Everything is
// registered with the controller-runtime registry, so it is served on the
// manager's metrics endpoint alongside the standard controller metrics.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const namespace = "remedik"

var (
	alertsReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "alerts_received_total",
		Help:      "Alerts accepted from Alertmanager deliveries.",
	})

	alertsTruncated = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "alerts_truncated_total",
		Help: "Alerts the sender dropped before delivery. A non-zero value means " +
			"remediation decisions were made on partial information.",
	})

	alertsUnmatched = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "alerts_unmatched_total",
		Help: "Firing alerts no strategy handled. A high rate means the strategies " +
			"do not cover what this cluster actually fires.",
	})

	ingestErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "ingest_errors_total",
		Help:      "Deliveries rejected by the gateway, by reason.",
	}, []string{"reason"})

	unauthorized = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "unauthorized_total",
		Help:      "Deliveries rejected by gateway authentication.",
	})

	recordsSwept = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "records_swept_total",
		Help:      "Terminal Remediation records reclaimed by the retention sweep.",
	})

	// A gauge rather than a counter: the question is how many records the
	// policy currently wants to delete and cannot, which is a level, not a
	// rate. A number that stays high means somebody configured a retention
	// they are not getting because a guard window is longer than it.
	recordsHeld = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "records_held_by_guards",
		Help:      "Records the retention sweep kept because a guard window still covers them.",
	})

	guardRejections = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "guard_rejections_total",
		Help:      "Executions refused by a guard, by strategy and guard.",
	}, []string{"strategy", "guard"})

	remediationsStarted = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "remediations_started_total",
		Help:      "Executions created, by strategy.",
	}, []string{"strategy"})

	remediationsFinished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "remediations_total",
		Help:      "Executions that reached a terminal state, by strategy and outcome.",
	}, []string{"strategy", "outcome"})

	remediationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "remediation_duration_seconds",
		Help:      "Wall-clock time from the first step starting to a terminal state.",
		// Remediations are seconds-to-minutes; the buckets span a single
		// patch through a run that retried its way to the backoff cap.
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 300, 900},
	}, []string{"strategy"})

	escalations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "escalations_total",
		Help: "onFailure plans that ran, by strategy and outcome. " +
			"outcome=\"Failed\" means a remediation failed and nobody was told.",
	}, []string{"strategy", "outcome"})
)

// MustRegister adds every remedik metric to the controller-runtime
// registry, so they are served on the manager's metrics endpoint.
//
// It panics on duplicate registration, which can only happen if it is
// called twice — a programming error, not a runtime condition.
func MustRegister() {
	metrics.Registry.MustRegister(collectors()...)
}

// collectors is the one list of what this package reports.
//
// It exists as a function so that a test can ask the same question Prometheus
// does — what metrics does this process serve — without registering them.
// The shipped Grafana dashboard and the shipped alert rules both name these
// metrics in files nothing compiles, so the agreement is checked instead.
func collectors() []prometheus.Collector {
	return []prometheus.Collector{
		alertsReceived,
		alertsTruncated,
		alertsUnmatched,
		ingestErrors,
		unauthorized,
		guardRejections,
		remediationsStarted,
		remediationsFinished,
		remediationDuration,
		escalations,
		recordsSwept,
		recordsHeld,
	}
}

// Gateway implements gateway.Recorder.
type Gateway struct{}

// AlertsReceived implements gateway.Recorder.
func (Gateway) AlertsReceived(n int) { alertsReceived.Add(float64(n)) }

// AlertsTruncated implements gateway.Recorder.
func (Gateway) AlertsTruncated(n int) { alertsTruncated.Add(float64(n)) }

// IngestError implements gateway.Recorder.
func (Gateway) IngestError(reason string) { ingestErrors.WithLabelValues(reason).Inc() }

// Unauthorized implements gateway.Recorder.
func (Gateway) Unauthorized() { unauthorized.Inc() }

// Engine implements engine.Recorder.
type Engine struct{}

// Unmatched implements engine.Recorder.
func (Engine) Unmatched() { alertsUnmatched.Inc() }

// GuardRejected implements engine.Recorder.
func (Engine) GuardRejected(strategy, guard string) {
	guardRejections.WithLabelValues(strategy, guard).Inc()
}

// RemediationStarted implements engine.Recorder.
func (Engine) RemediationStarted(strategy string) {
	remediationsStarted.WithLabelValues(strategy).Inc()
}

// RemediationFinished implements engine.Recorder.
func (Engine) RemediationFinished(strategy, outcome string, seconds float64) {
	remediationsFinished.WithLabelValues(strategy, outcome).Inc()
	remediationDuration.WithLabelValues(strategy).Observe(seconds)
}

// EscalationFinished implements engine.Recorder.
func (Engine) EscalationFinished(strategy, outcome string) {
	escalations.WithLabelValues(strategy, outcome).Inc()
}

// RecordsSwept implements engine.Recorder.
func (Engine) RecordsSwept(deleted, heldByGuards int) {
	recordsSwept.Add(float64(deleted))
	recordsHeld.Set(float64(heldByGuards))
}
