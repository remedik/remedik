package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// The counters in this package say what remedik has done. These say what it
// currently is: which build, which posture, how many strategies it is
// willing to act on, and how much is in flight. Without them a dashboard can
// draw a rate of remediations and still not answer "was it even allowed to
// act?", which is the first question anyone asks of a graph that is flat.

// scrapeTimeout bounds a snapshot. It reads the manager's cache, so anything
// slower than this means the cache is gone and the honest answer to
// Prometheus is a missing series rather than a hung scrape.
const scrapeTimeout = 5 * time.Second

// Snapshot is the operator's posture at scrape time.
type Snapshot struct {
	// StrategiesEnabled and StrategiesDisabled count RemediationStrategy
	// resources. Zero enabled is the difference between "nothing has
	// happened" and "nothing can happen".
	StrategiesEnabled  int
	StrategiesDisabled int

	// RecordsByState counts the Remediation resources that currently exist,
	// by state. It is a gauge of what is in the cluster now, not a rate:
	// history is pruned, so this is what an operator would see in
	// `kubectl get remediations`.
	RecordsByState map[string]int
}

// SnapshotFunc produces a Snapshot.
//
// It is called on the scrape path, so it must read from a cache and never
// from the API server: a scrape that hits the apiserver turns Prometheus's
// polling interval into load on the control plane.
type SnapshotFunc func(ctx context.Context) (Snapshot, error)

var (
	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Always 1; the version label carries the running build.",
	}, []string{"version"})

	dryRunGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "dry_run",
		Help: "1 when the operator's DEFAULT posture is dry-run, 0 when it acts. " +
			"A flat remediation rate means something different in each case. " +
			"Read it with remedik_namespace_posture: namespaces listed there " +
			"override this, so this gauge alone does not describe the cluster.",
	})

	namespacePostureGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "namespace_posture",
		Help: "Always 1, one series per namespace whose posture differs from the " +
			"default. posture=\"live\" means remedik acts there; posture=\"dryRun\" " +
			"means it only reports. No series means the default describes everything.",
	}, []string{"namespace", "posture"})

	strategiesDesc = prometheus.NewDesc(
		namespace+"_strategies",
		"RemediationStrategy resources, by whether they may match an alert.",
		[]string{"enabled"}, nil,
	)

	recordsDesc = prometheus.NewDesc(
		namespace+"_remediation_records",
		"Remediation resources currently in the cluster, by state. Terminal records are pruned, so this is what kubectl would show.",
		[]string{"state"}, nil,
	)
)

// PostureConfig describes the operator to the metrics that report on it.
type PostureConfig struct {
	// Version is the running build.
	Version string
	// DryRun is the operator's default posture.
	DryRun bool
	// NamespacePosture maps a namespace to "live" or "dryRun" for each one
	// that differs from the default. Reporting these is the whole point:
	// somebody reading dryRun=1 and concluding the cluster is safe is the
	// failure mode this feature introduces, and a metric they can query is
	// the cheapest way to keep that from being invisible.
	NamespacePosture map[string]string
	// Snapshot reads the live counts. Optional: without it, the two gauges
	// that need cluster state are simply not reported.
	Snapshot SnapshotFunc
	// Logger reports a snapshot that failed. Required when Snapshot is set.
	Logger *slog.Logger
}

// MustRegisterPosture adds the posture metrics to the controller-runtime
// registry. It panics on duplicate registration, which can only happen if it
// is called twice — a programming error, not a runtime condition.
func MustRegisterPosture(cfg PostureConfig) {
	ctrlmetrics.Registry.MustRegister(buildInfo, dryRunGauge, namespacePostureGauge)

	buildInfo.WithLabelValues(cfg.Version).Set(1)
	if cfg.DryRun {
		dryRunGauge.Set(1)
	} else {
		dryRunGauge.Set(0)
	}

	for ns, posture := range cfg.NamespacePosture {
		namespacePostureGauge.WithLabelValues(ns, posture).Set(1)
	}

	if cfg.Snapshot != nil {
		ctrlmetrics.Registry.MustRegister(&postureCollector{
			snapshot: cfg.Snapshot,
			logger:   cfg.Logger,
		})
	}
}

// postureCollector reports counts that are questions about now.
//
// It is a collector rather than a gauge updated on a timer because the
// answer is already in memory: the manager's cache holds every strategy and
// every record the operator watches, so reading it when Prometheus asks is
// both cheaper and more accurate than keeping a copy up to date.
type postureCollector struct {
	snapshot SnapshotFunc
	logger   *slog.Logger
}

// Describe implements prometheus.Collector.
func (c *postureCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- strategiesDesc
	ch <- recordsDesc
}

// Collect implements prometheus.Collector.
//
// A failed snapshot reports nothing rather than zero. Zero strategies and
// zero records are meaningful values — they say remediation cannot happen —
// and reporting them because a read failed would be a graph that lies
// quietly.
func (c *postureCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()

	snapshot, err := c.snapshot(ctx)
	if err != nil {
		if c.logger != nil {
			c.logger.Warn("could not read the operator's posture for a metrics scrape", "err", err)
		}
		return
	}

	ch <- prometheus.MustNewConstMetric(strategiesDesc, prometheus.GaugeValue,
		float64(snapshot.StrategiesEnabled), "true")
	ch <- prometheus.MustNewConstMetric(strategiesDesc, prometheus.GaugeValue,
		float64(snapshot.StrategiesDisabled), "false")

	for state, count := range snapshot.RecordsByState {
		ch <- prometheus.MustNewConstMetric(recordsDesc, prometheus.GaugeValue,
			float64(count), state)
	}
}

// Compile-time proof that the collector satisfies the interface.
var _ prometheus.Collector = (*postureCollector)(nil)
