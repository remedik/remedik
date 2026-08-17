package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/remedik/remedik/api/v1alpha1"
)

// Benchmarks at the scale the dashboard claims to work at.
//
// The claim is that it is usable on any cluster, and the auto-refresh means
// every open tab re-renders every ten seconds — so a page's cost is not paid
// once when somebody looks, it is paid continuously for as long as anybody has
// it open. That makes allocation per render the number that matters, not
// wall-clock on an idle laptop.
//
// Run with:
//
//	go test ./internal/dashboard/ -run XXX -bench . -benchmem

func benchCluster(records int) ([]v1alpha1.Remediation, []v1alpha1.RemediationStrategy) {
	rems := bigCluster(scaleNamespaces, scaleStrategies, records)
	strategies := make([]v1alpha1.RemediationStrategy, 0, scaleStrategies)
	for range scaleStrategies {
		strategies = append(strategies, enabledStrategy())
	}
	return rems, strategies
}

func BenchmarkBuildOverview(b *testing.B) {
	rems, strategies := benchCluster(scaleRecords)
	posture := Posture{DryRun: true, Live: []string{"ns-001"}}

	b.ReportAllocs()
	for b.Loop() {
		_ = buildOverview(rems, strategies, posture, testNow())
	}
}

func BenchmarkBuildRemediations(b *testing.B) {
	rems, _ := benchCluster(scaleRecords)

	b.ReportAllocs()
	for b.Loop() {
		_ = buildRemediations(rems, Filter{}, 1, testNow())
	}
}

func BenchmarkBuildRemediationsFiltered(b *testing.B) {
	rems, _ := benchCluster(scaleRecords)
	filter := Filter{Namespace: "ns-042"}

	b.ReportAllocs()
	for b.Loop() {
		_ = buildRemediations(rems, filter, 1, testNow())
	}
}

func BenchmarkBuildNamespaces(b *testing.B) {
	rems, _ := benchCluster(scaleRecords)
	posture := Posture{DryRun: true, Live: []string{"ns-001", "ns-002"}}

	b.ReportAllocs()
	for b.Loop() {
		_ = buildNamespaces(rems, posture, testNow())
	}
}

func BenchmarkBuildStrategies(b *testing.B) {
	rems, strategies := benchCluster(scaleRecords)

	b.ReportAllocs()
	for b.Loop() {
		_ = buildStrategies(strategies, rems, testNow())
	}
}

// The whole request, template execution included, which is what a viewer
// actually pays for.
func benchHandler(b *testing.B, path string, records int) {
	b.Helper()

	rems, strategies := benchCluster(records)
	h := benchDashboard(b, rems, strategies)
	req := httptest.NewRequest(http.MethodGet, path, nil)

	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

func BenchmarkServeOverview(b *testing.B)     { benchHandler(b, "/", scaleRecords) }
func BenchmarkServeRemediations(b *testing.B) { benchHandler(b, "/remediations", scaleRecords) }
func BenchmarkServeNamespaces(b *testing.B)   { benchHandler(b, "/namespaces", scaleRecords) }
func BenchmarkServeStrategies(b *testing.B)   { benchHandler(b, "/strategies", scaleRecords) }

func benchDashboard(
	b *testing.B, rems []v1alpha1.Remediation, strategies []v1alpha1.RemediationStrategy,
) http.Handler {
	b.Helper()

	h, err := New(Config{
		// deepCopy on, because that is what the manager's cache does and a
		// benchmark against a cheap fake measures the fake.
		Reader: &fakeReader{
			remediations: rems,
			strategies:   strategies,
			deepCopy:     true,
			honourUnsafe: true,
		},
		Namespace: testNamespace,
		Logger:    quietLogger(),
		Posture:   Posture{DryRun: true},
		Now:       testNow,
		Cluster:   "bench",
		Version:   "bench",
	})
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	return h
}
