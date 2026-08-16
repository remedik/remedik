package dashboard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ratyx/remedik/api/v1alpha1"
)

// newHandler builds a handler over the given fixtures.
func newHandler(t *testing.T, cfg Config) (*Handler, *fakeReader) {
	t.Helper()

	reader, _ := cfg.Reader.(*fakeReader)
	if reader == nil {
		reader = &fakeReader{}
		cfg.Reader = reader
	}
	if cfg.Namespace == "" {
		cfg.Namespace = testNamespace
	}
	if cfg.Logger == nil {
		cfg.Logger = quietLogger()
	}
	if cfg.Now == nil {
		cfg.Now = testNow
	}

	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	return h, reader
}

// get issues an authenticated GET and returns the response.
func get(t *testing.T, h *Handler, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mustContain(t *testing.T, body, want, why string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("the page does not %s: expected to find %q", why, want)
	}
}

func mustNotContain(t *testing.T, body, unwanted, why string) {
	t.Helper()
	if strings.Contains(body, unwanted) {
		t.Errorf("the page %s: did not expect to find %q", why, unwanted)
	}
}

// --------------------------------------------------------------------------
// Read-only by construction
// --------------------------------------------------------------------------

func TestMutatingMethodsAreRefused(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{
		remediations: []v1alpha1.Remediation{simulatedRemediation("sim-1", "deployment/payments/api", 10)},
		strategies:   []v1alpha1.RemediationStrategy{enabledStrategy()},
	}})

	methods := []string{
		http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, "PROPFIND",
	}
	// Every path, including ones that do not exist: the allowlist runs
	// before routing precisely so that a page added later cannot opt out.
	paths := []string{"/", "/strategies", "/remediations/sim-1", "/static/app.css", "/nope"}

	for _, method := range methods {
		for _, path := range paths {
			req := httptest.NewRequest(method, path, strings.NewReader("x=1"))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405", method, path, rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
				t.Errorf("%s %s Allow = %q, want %q", method, path, allow, "GET, HEAD")
			}
		}
	}
}

func TestHeadServesHeadersWithoutABody(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{
		strategies: []v1alpha1.RemediationStrategy{enabledStrategy()},
	}})

	req := httptest.NewRequest(http.MethodHead, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD / = %d, want 200", rec.Code)
	}
	if got := rec.Body.Len(); got != 0 {
		t.Errorf("HEAD / wrote %d bytes of body, want 0", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("HEAD / Content-Type = %q, want text/html", ct)
	}
}

func TestTheDashboardOnlyReadsItsOwnNamespace(t *testing.T) {
	h, reader := newHandler(t, Config{Reader: &fakeReader{
		remediations: []v1alpha1.Remediation{succeededRemediation("ok-1", 5)},
		strategies:   []v1alpha1.RemediationStrategy{enabledStrategy()},
	}})

	get(t, h, "/", nil)
	get(t, h, "/strategies", nil)

	var sawRemediationList bool
	for _, ns := range reader.listedNamespaces {
		if ns == "" {
			continue // the strategies list: they are cluster-scoped
		}
		sawRemediationList = true
		if ns != testNamespace {
			t.Errorf("listed remediations in namespace %q, want %q", ns, testNamespace)
		}
	}
	if !sawRemediationList {
		t.Error("no namespaced list was issued; the remediations were never scoped")
	}
}

// --------------------------------------------------------------------------
// Authentication
// --------------------------------------------------------------------------

func TestAuthentication(t *testing.T) {
	records := []v1alpha1.Remediation{simulatedRemediation("sim-secret", "deployment/payments/api", 10)}

	tests := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{name: "no credentials", want: http.StatusUnauthorized},
		{
			name:   "wrong bearer token",
			header: map[string]string{"Authorization": "Bearer nope"},
			want:   http.StatusUnauthorized,
		},
		{
			name:   "unsupported scheme",
			header: map[string]string{"Authorization": "Digest " + testToken},
			want:   http.StatusUnauthorized,
		},
		{
			name:   "malformed basic credentials",
			header: map[string]string{"Authorization": "Basic not-base64!"},
			want:   http.StatusUnauthorized,
		},
		{
			name:   "correct bearer token",
			header: map[string]string{"Authorization": "Bearer " + testToken},
			want:   http.StatusOK,
		},
		{
			// A browser cannot be told to send a bearer token, and the
			// documented way in is a port-forward and a browser.
			name:   "correct token as a basic password",
			header: map[string]string{"Authorization": basicAuth("", testToken)},
			want:   http.StatusOK,
		},
		{
			name:   "correct token as a basic password with any username",
			header: map[string]string{"Authorization": basicAuth("sre", testToken)},
			want:   http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newHandler(t, Config{
				Reader: &fakeReader{remediations: records},
				Token:  testToken,
			})

			rec := get(t, h, "/", tc.header)
			if rec.Code != tc.want {
				t.Fatalf("GET / = %d, want %d", rec.Code, tc.want)
			}
			if tc.want != http.StatusUnauthorized {
				return
			}

			if challenge := rec.Header().Get("WWW-Authenticate"); challenge == "" {
				t.Error("a 401 carried no WWW-Authenticate challenge")
			}
			// The refusal must not be a disclosure.
			mustNotContain(t, rec.Body.String(), "sim-secret",
				"leaked a remediation name to an unauthenticated request")
			mustNotContain(t, rec.Body.String(), "payments",
				"leaked a target namespace to an unauthenticated request")
		})
	}
}

func TestNoTokenMeansNoAuthentication(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{
		strategies: []v1alpha1.RemediationStrategy{enabledStrategy()},
	}})

	if rec := get(t, h, "/", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200 when no token is configured", rec.Code)
	}
}

func basicAuth(user, password string) string {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth(user, password)
	return req.Header.Get("Authorization")
}

// --------------------------------------------------------------------------
// Overview
// --------------------------------------------------------------------------

func TestOverviewRendersEveryOutcome(t *testing.T) {
	h, _ := newHandler(t, Config{
		Reader: &fakeReader{
			remediations: []v1alpha1.Remediation{
				simulatedRemediation("sim-1", "deployment/payments/api", 30),
				simulatedRemediation("sim-2", "deployment/payments/worker", 20),
				succeededRemediation("ok-1", 15),
				failedRemediation("bad-1", 10),
				pendingRemediation("new-1", 1),
			},
			strategies: []v1alpha1.RemediationStrategy{enabledStrategy(), disabledStrategy()},
		},
		DryRun: true,
	})

	rec := get(t, h, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, name := range []string{"sim-1", "sim-2", "ok-1", "bad-1", "new-1"} {
		mustContain(t, body, name, "list every recent execution")
	}
	for _, state := range []string{"Simulated", "Succeeded", "Failed", "Pending"} {
		mustContain(t, body, ">"+state+"<", "show the "+state+" state")
	}
	mustContain(t, body, "deployment/payments/api", "show the target")
	mustContain(t, body, "KubePodCrashLooping", "name the triggering alert")
	mustContain(t, body, `href="/remediations/bad-1"`, "link each record to its detail page")

	// Cache-Control matters here: a cached overview is a stale incident.
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestOverviewSummarisesADryRunTrial(t *testing.T) {
	h, _ := newHandler(t, Config{
		Reader: &fakeReader{
			remediations: []v1alpha1.Remediation{
				simulatedRemediation("sim-1", "deployment/payments/api", 60*24*2),
				simulatedRemediation("sim-2", "deployment/payments/api", 90),
				simulatedRemediation("sim-3", "deployment/payments/worker", 30),
			},
			strategies: []v1alpha1.RemediationStrategy{enabledStrategy()},
		},
		DryRun: true,
	})

	body := get(t, h, "/", nil).Body.String()

	mustContain(t, body, "What remedik would have done", "lead with the dry-run report")
	mustContain(t, body, "Dry-run is on", "say that dry-run is active")
	mustContain(t, body, "<strong>3</strong>", "count the simulated remediations")
	mustContain(t, body, "<strong>2</strong> target", "count the distinct targets")
	mustContain(t, body, "2 days", "state the period the trial covers")
	mustContain(t, body, "pod-crashloop", "break the total down by strategy")
	mustContain(t, body, "patch deployment deployment/payments/worker restartedAt annotation",
		"show what the most recent simulation would have done")
	mustContain(t, body, "--set dryRun=false", "say how to act on the report")
}

func TestOverviewWithoutDryRunHasNoReport(t *testing.T) {
	h, _ := newHandler(t, Config{
		Reader: &fakeReader{
			remediations: []v1alpha1.Remediation{succeededRemediation("ok-1", 5)},
			strategies:   []v1alpha1.RemediationStrategy{enabledStrategy()},
		},
	})

	body := get(t, h, "/", nil).Body.String()
	mustNotContain(t, body, "What remedik would have done",
		"shows a dry-run report with no simulations and dry-run off")
	mustContain(t, body, "Live", "state that the operator is acting")
}

func TestOverviewEmptyStates(t *testing.T) {
	tests := []struct {
		name       string
		strategies []v1alpha1.RemediationStrategy
		want       string
	}{
		{
			name: "no strategies at all",
			want: "No strategies, so nothing can run",
		},
		{
			name:       "every strategy disabled",
			strategies: []v1alpha1.RemediationStrategy{disabledStrategy()},
			want:       "Every strategy is disabled",
		},
		{
			name:       "enabled, but nothing has matched",
			strategies: []v1alpha1.RemediationStrategy{enabledStrategy()},
			want:       "Nothing has run yet",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newHandler(t, Config{Reader: &fakeReader{strategies: tc.strategies}})

			body := get(t, h, "/", nil).Body.String()
			mustContain(t, body, tc.want, "explain the empty cluster")
			mustNotContain(t, body, "<tbody>", "renders an empty table instead of an explanation")
		})
	}
}

func TestOverviewCapsTheListAndSaysSo(t *testing.T) {
	records := make([]v1alpha1.Remediation, 0, recentLimit+5)
	for i := range recentLimit + 5 {
		records = append(records, succeededRemediation("ok-"+strconv.Itoa(i), i))
	}

	h, _ := newHandler(t, Config{Reader: &fakeReader{remediations: records}})
	body := get(t, h, "/", nil).Body.String()

	mustContain(t, body, "showing 50 of 55", "state how much of the history is listed")
	mustContain(t, body, "5 older records not listed", "say what was left out")
}

// --------------------------------------------------------------------------
// Remediation detail
// --------------------------------------------------------------------------

func TestRemediationDetailExplainsAFailure(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{
		remediations: []v1alpha1.Remediation{failedRemediation("bad-1", 10)},
	}})

	rec := get(t, h, "/remediations/bad-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /remediations/bad-1 = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	mustContain(t, body, "Step 2 (deployment.restart) failed", "name the failing step")
	mustContain(t, body, `deployments.apps &#34;worker&#34; not found`, "show the action's message")
	mustContain(t, body, "Skipped", "show which steps never ran")
	mustContain(t, body, "2 of 2", "state which attempt this was, out of the budget")
	mustContain(t, body, "StepFailed", "name the terminal reason")
	mustContain(t, body, "restarted deployment payments/api", "show the step that did work")
}

func TestRemediationDetailShowsHowTheChangeWasMade(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{
		remediations: []v1alpha1.Remediation{succeededRemediation("ok-1", 5)},
	}})

	body := get(t, h, "/remediations/ok-1", nil).Body.String()

	// The command a human would have typed is the one thing on this page a
	// reader can check against what they already know.
	mustContain(t, body, "kubectl rollout restart deployment/api -n payments",
		"show the equivalent command")
	// A step that ran but did not fix anything is not a success, so the
	// check's finding belongs next to the step.
	mustContain(t, body, "3/3 replicas updated, available and ready",
		"show what the post-condition check confirmed")
	mustContain(t, body, "deployment/checkout/web", "name the object the step acted on")
	mustContain(t, body, "replicas", "show the action's structured outputs")
}

func TestRemediationDetailShowsASimulatedPlan(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{
		remediations: []v1alpha1.Remediation{
			simulatedRemediation("sim-1", "deployment/payments/api", 10),
		},
	}})

	body := get(t, h, "/remediations/sim-1", nil).Body.String()

	mustContain(t, body, "The plan", "call the step list a plan for a simulated run")
	mustContain(t, body, "nothing in the cluster was changed", "say that nothing was touched")
	mustContain(t, body, "patch deployment deployment/payments/api restartedAt annotation",
		"show what would have been done")
	mustContain(t, body, "KubePodCrashLooping", "name the triggering alert")
	mustContain(t, body, "severity", "list the alert's labels")
}

func TestRemediationDetailExplainsAnInterruptedRun(t *testing.T) {
	rem := failedRemediation("interrupted-1", 10)
	rem.Status.Reason = v1alpha1.ReasonInterrupted
	rem.Status.Message = "the operator restarted while this attempt was running"

	h, _ := newHandler(t, Config{Reader: &fakeReader{
		remediations: []v1alpha1.Remediation{rem},
	}})

	body := get(t, h, "/remediations/interrupted-1", nil).Body.String()
	mustContain(t, body, "failed rather than resumed",
		"explain why an interrupted run is not retried in place")
}

func TestUnknownRemediationIs404(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}})

	rec := get(t, h, "/remediations/ghost", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /remediations/ghost = %d, want 404", rec.Code)
	}
	mustContain(t, rec.Body.String(), "pruned",
		"explain that terminal records do not live forever")
}

func TestRemediationsIndexRedirectsToTheOverview(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}})

	rec := get(t, h, "/remediations/", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /remediations/ = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

// --------------------------------------------------------------------------
// Strategies
// --------------------------------------------------------------------------

func TestStrategiesPage(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{
		strategies: []v1alpha1.RemediationStrategy{enabledStrategy(), disabledStrategy()},
		remediations: []v1alpha1.Remediation{
			simulatedRemediation("sim-1", "deployment/payments/api", 30),
			failedRemediation("bad-1", 10),
		},
	}})

	rec := get(t, h, "/strategies", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /strategies = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	mustContain(t, body, "pod-crashloop", "list the enabled strategy")
	mustContain(t, body, "node-drain", "list the disabled strategy")
	mustContain(t, body, ">Disabled<", "mark a disabled strategy as disabled")
	mustContain(t, body, "is-disabled", "style a disabled strategy differently")
	mustContain(t, body, "KubePodCrashLooping", "show the matchers")
	mustContain(t, body, "15m", "show the cooldown guard")
	mustContain(t, body, "deployment.restart", "show the steps")
	mustContain(t, body, "last run", "show when the strategy last ran")
	mustContain(t, body, "None. Both guards are opt-in",
		"say plainly that a strategy has no guards")
}

func TestStrategiesEmptyState(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}})

	body := get(t, h, "/strategies", nil).Body.String()
	mustContain(t, body, "No strategies in this cluster", "explain an empty list")
}

// --------------------------------------------------------------------------
// Failure modes
// --------------------------------------------------------------------------

func TestAFailedReadIs503(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{
		listErr: errors.New("the cache is not started"),
	}})

	for _, path := range []string{"/", "/strategies"} {
		rec := get(t, h, path, nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503", path, rec.Code)
		}
		mustContain(t, rec.Body.String(), "the cache is not started",
			"say why the cluster could not be read")
	}
}

func TestUnknownPathIs404(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}})

	rec := get(t, h, "/metrics", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /metrics = %d, want 404", rec.Code)
	}
	mustContain(t, rec.Body.String(), "Page not found", "render a readable 404")
}

func TestNewRejectsAnIncompleteConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "no reader", cfg: Config{Namespace: "remedik", Logger: quietLogger()}},
		{name: "no namespace", cfg: Config{Reader: &fakeReader{}, Logger: quietLogger()}},
		{name: "no logger", cfg: Config{Reader: &fakeReader{}, Namespace: "remedik"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Error("New() error = nil, want an error")
			}
		})
	}
}
