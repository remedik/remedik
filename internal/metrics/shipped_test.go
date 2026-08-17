package metrics

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// The Grafana dashboard and the alert rules are shipped in the chart, name
// these metrics in files nothing compiles, and are the two places a user meets
// them. Renaming a metric would leave a panel drawing an empty graph and an
// alert that can never fire — both of which look like a healthy cluster.
//
// This is the same class of defect as the CRD enums: two lists of the same
// thing, in different files, kept in step by hand. So it is checked the same
// way, as a claim about files.

// promQLMetric matches a metric name as PromQL would read it, including the
// suffixes Prometheus derives from a histogram.
var promQLMetric = regexp.MustCompile(`\bremedik_[a-z_]+`)

func TestTheDashboardOnlyQueriesMetricsThatExist(t *testing.T) {
	served := servedMetrics(t)

	for name, where := range shippedMetricNames(t, "dashboards/remedik.json") {
		if !served[baseName(name)] {
			t.Errorf("the dashboard queries %q, which this process does not serve, "+
				"so the panel draws an empty graph: %s", name, where)
		}
	}
}

func TestTheAlertRulesOnlyFireOnMetricsThatExist(t *testing.T) {
	served := servedMetrics(t)

	for name, where := range shippedMetricNames(t, "templates/prometheusrule.yaml") {
		if !served[baseName(name)] {
			t.Errorf("an alert rule is written on %q, which this process does not "+
				"serve, so the rule can never fire: %s", name, where)
		}
	}
}

// And the other direction: a metric nobody can see is a metric nobody uses.
//
// Not a style rule. Every metric here costs a series in somebody's Prometheus,
// and one that appears in no dashboard and no alert is either something a user
// is expected to graph themselves — which is a documentation obligation — or
// something that should not be exported at all.
func TestEveryServedMetricIsReachableFromSomethingShipped(t *testing.T) {
	// Named individually, with the reason, so that adding a metric is a
	// decision about who reads it rather than an omission.
	reachedByOtherMeans := map[string]string{
		// Charted as the numerator of the success ratio and as the base of
		// remediations_total; a panel of its own would say the same thing.
		"remedik_remediations_started_total": "the dashboard charts the outcome, not the start",
		// The dashboard's namespace variable is built from it, so it is queried
		// without appearing in a panel's expression.
		"remedik_namespace_posture": "it populates the dashboard's namespace picker",
	}

	shipped := map[string]bool{}
	for _, file := range []string{"dashboards/remedik.json", "templates/prometheusrule.yaml"} {
		for name := range shippedMetricNames(t, file) {
			shipped[baseName(name)] = true
		}
	}

	for name := range servedMetrics(t) {
		if shipped[name] || reachedByOtherMeans[name] != "" {
			continue
		}
		t.Errorf("%q is exported and appears in no dashboard panel and no alert "+
			"rule. Chart it, alert on it, drop it, or say in "+
			"TestEveryServedMetricIsReachableFromSomethingShipped how a user is "+
			"meant to find it.", name)
	}
}

// servedMetrics is every metric name this process exports, asked the way
// Prometheus asks: by describing the collectors rather than by reading the
// source. A metric renamed in a Name: field is renamed here too.
func servedMetrics(t *testing.T) map[string]bool {
	t.Helper()

	all := append(collectors(), postureCollectors()...)
	// The snapshot collector is registered conditionally, but the metrics it
	// describes are part of the contract either way.
	all = append(all, &postureCollector{})

	names := map[string]bool{}
	for _, c := range all {
		descs := make(chan *prometheus.Desc, 64)
		go func(c prometheus.Collector) {
			c.Describe(descs)
			close(descs)
		}(c)
		for desc := range descs {
			// A Desc prints as `Desc{fqName: "x", help: ...}`; the fqName is
			// the only part with a stable, documented shape.
			s := desc.String()
			const key = `fqName: "`
			i := strings.Index(s, key)
			if i < 0 {
				t.Fatalf("could not read a metric name out of %q; the "+
					"client_golang Desc format changed", s)
			}
			rest := s[i+len(key):]
			names[rest[:strings.IndexByte(rest, '"')]] = true
		}
	}
	if len(names) == 0 {
		t.Fatal("no metrics were described at all; the collector lists are empty")
	}
	return names
}

// shippedMetricNames maps each metric a shipped file names to the line it is
// on, so a failure says where to look rather than only what is wrong.
func shippedMetricNames(t *testing.T, rel string) map[string]string {
	t.Helper()

	path := filepath.Join("..", "..", "charts", "remedik", rel)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	found := map[string]string{}
	for n, line := range strings.Split(string(body), "\n") {
		for _, name := range promQLMetric.FindAllString(line, -1) {
			if _, seen := found[name]; !seen {
				found[name] = filepath.ToSlash(filepath.Join("charts/remedik", rel)) +
					":" + itoa(n+1)
			}
		}
	}
	if len(found) == 0 {
		t.Fatalf("%s names no remedik metric at all; it was emptied or moved", rel)
	}
	return found
}

// baseName strips the suffixes Prometheus derives, so that a query on
// remediation_duration_seconds_bucket is understood as a query on the
// histogram the code registers.
func baseName(name string) string {
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

// itoa avoids pulling strconv in for one call site in a test helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
