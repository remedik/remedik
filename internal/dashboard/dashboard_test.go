package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/remedik/remedik/api/v1alpha1"
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
		Posture: Posture{DryRun: true},
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
		Posture: Posture{DryRun: true},
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
			want:       "Nothing has matched yet",
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

// The overview shows a short tail and sends the reader to the list, which is
// the whole point of the list existing.
func TestOverviewShowsATailAndLinksToTheList(t *testing.T) {
	records := make([]v1alpha1.Remediation, 0, recentLimit+5)
	for i := range recentLimit + 5 {
		records = append(records, succeededRemediation("ok-"+strconv.Itoa(i), i))
	}

	h, _ := newHandler(t, Config{Reader: &fakeReader{remediations: records}})
	body := get(t, h, "/", nil).Body.String()

	if rows := strings.Count(body, `<tr>`); rows > recentLimit+4 {
		t.Errorf("the overview drew %d rows; it is a summary, not the list", rows)
	}
	mustContain(t, body, `href="/remediations"`, "link to the full list")
	mustContain(t, body, "All 13 remediations", "say how many there are in total")
}

// The list pages rather than truncating. "200 shown, 9,800 not drawn" is not
// a list of what happened; it is a truncation with an apology.
func TestRemediationsListPages(t *testing.T) {
	records := make([]v1alpha1.Remediation, 0, pageSize+5)
	for i := range pageSize + 5 {
		records = append(records, succeededRemediation("ok-"+strconv.Itoa(i), i))
	}

	h, _ := newHandler(t, Config{Reader: &fakeReader{remediations: records}})

	first := get(t, h, "/remediations", nil).Body.String()
	mustContain(t, first, "Page 1 of 2", "say where the reader is")
	mustContain(t, first, `href="/remediations?page=2"`, "offer the next page")

	second := get(t, h, "/remediations?page=2", nil).Body.String()
	mustContain(t, second, "Page 2 of 2", "say where the reader is on page two")
	mustContain(t, second, "101&ndash;105 of 105", "say which rows are drawn")

	// A bookmarked page beyond the end shows the last page rather than an
	// error: history is pruned, so yesterday's page 40 is today's nothing.
	beyond := get(t, h, "/remediations?page=99", nil)
	if beyond.Code != http.StatusOK {
		t.Errorf("GET ?page=99 = %d, want 200", beyond.Code)
	}
	mustContain(t, beyond.Body.String(), "Page 2 of 2", "clamp to the last page")
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

func TestRemediationsIndexIsTheList(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{
		remediations: []v1alpha1.Remediation{succeededRemediation("ok-1", 5)},
	}})

	rec := get(t, h, "/remediations/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /remediations/ = %d, want 200", rec.Code)
	}
	mustContain(t, rec.Body.String(), "ok-1", "list the executions")
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

// A bug in a view builder must not become a closed connection. This is the
// page somebody opens when something is already wrong.
func TestPanicRendersAPageRatherThanNothing(t *testing.T) {
	h, _ := newHandler(t, Config{Reader: &fakeReader{}})

	// A reader that panics stands in for any builder bug; the real one was
	// an empty filter result making a row loop start at index -1.
	h.reader = panickingReader{}

	rec := get(t, h, "/", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET / = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	mustContain(t, body, "Something went wrong", "say that the page failed")
	mustContain(t, body, "nothing about your cluster has changed",
		"reassure the reader that the dashboard only reads")
}

type panickingReader struct{ client.Reader }

func (panickingReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	panic("a builder bug")
}

// The dashboard lists with client.UnsafeDisableDeepCopy, which hands it the
// manager's own objects rather than copies. That removes about thirteen
// megabytes and a hundred and thirty thousand allocations from every render —
// and it is only safe while this package never writes to a listed object,
// because a write would corrupt the cache every controller reads from.
//
// So the guarantee is checked rather than remembered: every page is rendered
// against records whose contents are compared before and after.
func TestTheDashboardNeverMutatesWhatItReads(t *testing.T) {
	records := []v1alpha1.Remediation{
		succeededRemediation("ok", 5),
		failedRemediation("bad", 4),
		simulatedRemediation("dry", "deployment/payments/api", 3),
		pendingRemediation("waiting", 2),
	}
	strategies := []v1alpha1.RemediationStrategy{enabledStrategy(), disabledStrategy()}

	// A deep copy taken before anything is served, to compare against.
	want := make([]v1alpha1.Remediation, len(records))
	for i := range records {
		want[i] = *records[i].DeepCopy()
	}
	wantStrategies := make([]v1alpha1.RemediationStrategy, len(strategies))
	for i := range strategies {
		wantStrategies[i] = *strategies[i].DeepCopy()
	}

	h, _ := newHandler(t, Config{
		Reader: &fakeReader{remediations: records, strategies: strategies},
	})

	for _, path := range []string{
		"/", "/remediations", "/remediations?namespace=payments&state=Failed",
		"/remediations?page=2", "/namespaces", "/strategies",
		"/remediations/ok", "/remediations/bad",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
	}

	// The order may differ: sortNewestFirst reorders the slice this package
	// owns, which is allowed. The contents may not.
	byName := map[string]*v1alpha1.Remediation{}
	for i := range records {
		byName[records[i].Name] = &records[i]
	}
	for i := range want {
		got, ok := byName[want[i].Name]
		if !ok {
			t.Fatalf("record %q disappeared from the slice", want[i].Name)
		}
		if !reflect.DeepEqual(*got, want[i]) {
			t.Errorf("record %q was modified while serving a page.\n"+
				"The dashboard lists with UnsafeDisableDeepCopy, so a write here "+
				"corrupts the manager's cache for every controller. Either stop "+
				"writing, or drop the option in listRemediations.", want[i].Name)
		}
	}
	for i := range strategies {
		if !reflect.DeepEqual(strategies[i], wantStrategies[i]) {
			t.Errorf("strategy %q was modified while serving a page", strategies[i].Name)
		}
	}
}

// And the option is actually asked for, since the test above would pass just
// as well if it were quietly dropped.
func TestListsAreZeroCopy(t *testing.T) {
	// New directly: the shared helper swaps in its own fakeReader when the
	// one it is given is not that type.
	reader := &recordingReader{}
	h, err := New(Config{
		Reader:    reader,
		Namespace: testNamespace,
		Logger:    quietLogger(),
		Now:       testNow,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if len(reader.optionsSeen) == 0 {
		t.Fatal("no List was made")
	}
	for i, opts := range reader.optionsSeen {
		if opts.UnsafeDisableDeepCopy == nil || !*opts.UnsafeDisableDeepCopy {
			t.Errorf("List %d did not pass client.UnsafeDisableDeepCopy, so every "+
				"render deep-copies every record again", i)
		}
	}
}

// recordingReader captures the options each List was given.
type recordingReader struct {
	optionsSeen []client.ListOptions
}

func (r *recordingReader) Get(
	context.Context, client.ObjectKey, client.Object, ...client.GetOption,
) error {
	return nil
}

func (r *recordingReader) List(
	_ context.Context, _ client.ObjectList, opts ...client.ListOption,
) error {
	var options client.ListOptions
	for _, opt := range opts {
		opt.ApplyToList(&options)
	}
	r.optionsSeen = append(r.optionsSeen, options)
	return nil
}

// Paused is on the chrome rather than one page, because it is the answer to
// "why is nothing happening" and somebody asking that is on whichever page they
// happened to be looking at.
func TestPausedIsOnEveryPage(t *testing.T) {
	h, err := New(Config{
		Reader:    &fakeReader{remediations: []v1alpha1.Remediation{succeededRemediation("a", 5)}},
		Namespace: testNamespace,
		Logger:    quietLogger(),
		Now:       testNow,
		Posture:   Posture{DryRun: false},
		Paused:    func() (bool, string) { return true, "network incident" },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, path := range []string{"/", "/remediations", "/namespaces", "/strategies"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		body := rec.Body.String()

		if !strings.Contains(body, "Paused") {
			t.Errorf("GET %s does not say remediation is paused", path)
		}
		if !strings.Contains(body, "network incident") {
			t.Errorf("GET %s does not say why", path)
		}
		// It overrides the posture, so the page must not also claim to be live.
		if strings.Contains(body, ">Live<") {
			t.Errorf("GET %s claims Live while paused", path)
		}
	}
}

// And with no pause the chrome is unchanged, so the field stays optional.
func TestNotPausedSaysNothingAboutIt(t *testing.T) {
	h, _ := newHandler(t, Config{Posture: Posture{DryRun: false}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()

	if strings.Contains(body, "mode-paused") {
		t.Error("the paused chip is rendered with no pause configured")
	}
	if !strings.Contains(body, ">Live<") {
		t.Error("the posture chip disappeared when nothing was paused")
	}
}
