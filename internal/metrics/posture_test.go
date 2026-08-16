package metrics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// collect renders a collector the way a scrape would see it: one line per
// series, so an assertion reads like the thing Prometheus stores.
func collect(t *testing.T, snapshot SnapshotFunc) string {
	t.Helper()

	registry := prometheus.NewRegistry()
	registry.MustRegister(&postureCollector{snapshot: snapshot, logger: quietLogger()})

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	var out strings.Builder
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			out.WriteString(family.GetName())
			for _, label := range metric.GetLabel() {
				out.WriteString("{" + label.GetName() + "=" + label.GetValue() + "}")
			}
			out.WriteString(" " + strconv.FormatFloat(metric.GetGauge().GetValue(), 'f', -1, 64))
			out.WriteString("\n")
		}
	}
	return out.String()
}

func TestPostureCollectorReportsTheCurrentState(t *testing.T) {
	got := collect(t, func(context.Context) (Snapshot, error) {
		return Snapshot{
			StrategiesEnabled:  3,
			StrategiesDisabled: 1,
			RecordsByState: map[string]int{
				"Succeeded": 12,
				"Simulated": 40,
				"Pending":   2,
			},
		}, nil
	})

	for _, want := range []string{
		"remedik_strategies{enabled=true} 3",
		"remedik_strategies{enabled=false} 1",
		"remedik_remediation_records{state=Succeeded} 12",
		"remedik_remediation_records{state=Simulated} 40",
		"remedik_remediation_records{state=Pending} 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("scrape is missing %q; got:\n%s", want, got)
		}
	}
}

// Zero enabled strategies means remediation cannot happen — a real and
// alarming value. Reporting it because a read failed would turn a monitoring
// failure into a false incident.
func TestPostureCollectorReportsNothingWhenItCannotRead(t *testing.T) {
	got := collect(t, func(context.Context) (Snapshot, error) {
		return Snapshot{}, errors.New("the cache is not started")
	})

	if strings.Contains(got, "remedik_strategies") {
		t.Errorf("a failed snapshot still reported strategies:\n%s", got)
	}
	if strings.Contains(got, "remedik_remediation_records") {
		t.Errorf("a failed snapshot still reported records:\n%s", got)
	}
}

func TestPostureCollectorBoundsItsRead(t *testing.T) {
	var hadDeadline bool
	collect(t, func(ctx context.Context) (Snapshot, error) {
		_, hadDeadline = ctx.Deadline()
		return Snapshot{RecordsByState: map[string]int{}}, nil
	})

	// A scrape that hangs is worse than a scrape that reports nothing: it
	// holds a Prometheus connection and delays every other target behind it.
	if !hadDeadline {
		t.Error("the snapshot ran without a deadline")
	}
}

func TestPostureGaugesDescribeTheOperator(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(buildInfo, dryRunGauge)

	buildInfo.Reset()
	buildInfo.WithLabelValues("v0.1.0").Set(1)
	dryRunGauge.Set(1)

	if got := testutil.ToFloat64(dryRunGauge); got != 1 {
		t.Errorf("remedik_dry_run = %v, want 1", got)
	}
	if got := testutil.ToFloat64(buildInfo.WithLabelValues("v0.1.0")); got != 1 {
		t.Errorf("remedik_build_info = %v, want 1", got)
	}
}
