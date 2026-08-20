package dashboard

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/remedik/remedik/api/v1alpha1"
)

func TestLinks_CarryTheRecordsOwnValues(t *testing.T) {
	rem := failedRemediation("run-1", 30)
	links := []Link{{
		Name: "Grafana",
		URL: "https://grafana.example.com/d/k8s?var-namespace={namespace}" +
			"&var-alert={alert}&from={from}&to={to}",
	}}

	resolved := resolveLinks(links, &rem, testNow())
	if len(resolved) != 1 {
		t.Fatalf("resolved %d links, want 1", len(resolved))
	}
	url := resolved[0].URL

	for _, want := range []string{
		"var-namespace=payments",
		"var-alert=KubePodCrashLooping",
		"from=2026-08-16T11%3A15%3A00Z",
	} {
		if !strings.Contains(url, want) {
			t.Errorf("link %q does not carry %q", url, want)
		}
	}
	if strings.Contains(url, "{") {
		t.Errorf("link %q still has a placeholder in it", url)
	}
}

// A workload called "api&admin=1" must not become two query parameters, and a
// target carries slashes that mean something in a path and nothing in a value.
func TestLinks_ValuesAreEscapedOnTheWayIn(t *testing.T) {
	rem := failedRemediation("run-1", 5)
	rem.Spec.Target = "deployment/payments/api&admin=1"

	resolved := resolveLinks([]Link{{
		Name: "Logs", URL: "https://logs.example.com/?q={target}",
	}}, &rem, testNow())

	url := resolved[0].URL
	if strings.Contains(url, "&admin=1") {
		t.Errorf("link %q lets a workload name add a parameter", url)
	}
	if strings.Contains(url, "payments/api") {
		t.Errorf("link %q leaves the target's slashes unescaped", url)
	}
}

// The person writing values.yaml is trusted with the cluster. The template is
// still rendered into a page, and an unchecked scheme there is a javascript:
// URL waiting for a reader who trusts this dashboard.
func TestLinks_AHostileTemplateNeverReachesAPage(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))

	kept := validLinks([]Link{
		{Name: "Fine", URL: "https://grafana.example.com/d/k8s"},
		{Name: "Also fine", URL: "http://in-cluster.svc/graph"},
		{Name: "Hostile", URL: "javascript:alert(document.cookie)"},
		{Name: "Local", URL: "file:///etc/passwd"},
		{Name: "Sneaky", URL: "JavaScript:alert(1)"},
		{Name: "", URL: "https://nameless.example.com"},
		// A newline in a chart value used to end its own YAML list item and
		// begin another one: an extra flag on the operator's command line,
		// written by whoever wrote the values file.
		{Name: "Injected\n            - --actions=job.run", URL: "https://x.example.com"},
		{Name: "Tabbed", URL: "https://x.example.com/\tsomewhere"},
	}, logger)

	if len(kept) != 2 {
		t.Fatalf("kept %d links, want the two http ones: %+v", len(kept), kept)
	}
	for _, link := range kept {
		if !strings.HasPrefix(link.URL, "http") {
			t.Errorf("kept %q", link.URL)
		}
	}
	// Dropped loudly: a mistyped link is not a reason to refuse to remediate a
	// cluster, and it is a reason to say so where somebody is looking.
	for _, name := range []string{"Hostile", "Local", "Sneaky", "Injected", "Tabbed"} {
		if !strings.Contains(log.String(), name) {
			t.Errorf("dropping %s was not logged", name)
		}
	}
}

// The window is padded, because a dashboard opened at exactly the moment
// something happened shows it against no context at all.
func TestLinks_TheWindowIsPaddedAroundTheRecord(t *testing.T) {
	rem := failedRemediation("run-1", 30)
	from, to := linkWindow(&rem, testNow())

	if got := rem.CreationTimestamp.Sub(from); got != linkPad {
		t.Errorf("window starts %v before the record, want %v", got, linkPad)
	}
	if !to.After(rem.Status.CompletedAt.Time) {
		t.Errorf("window ends at %v, before the record completed", to)
	}
	if !from.Before(to) {
		t.Errorf("window runs backwards: %v to %v", from, to)
	}
}

// A record still running has no completion, and the window ends now rather
// than at the zero time.
func TestLinks_ARunningRecordEndsNow(t *testing.T) {
	rem := pendingRemediation("new-1", 2)
	_, to := linkWindow(&rem, testNow())

	if want := testNow().Add(linkPad); !to.Equal(want) {
		t.Errorf("window ends at %v, want %v", to, want)
	}
}

// No page depends on a link existing.
func TestLinks_PagesRenderWithNoneConfigured(t *testing.T) {
	h, reader := newHandler(t, Config{})
	reader.remediations = []v1alpha1.Remediation{failedRemediation("run-1", 5)}

	body := get(t, h, "/remediations/run-1", nil).Body.String()
	mustNotContain(t, body, `class="links"`, "render an empty links row")
	mustContain(t, body, "run-1", "still render the record")
}

// And the whole way through: configured on the handler, rendered on the page.
func TestLinks_ReachThePage(t *testing.T) {
	h, reader := newHandler(t, Config{
		Links: []Link{
			{Name: "Grafana", URL: "https://grafana.example.com/d/k8s?ns={namespace}"},
			{Name: "Nope", URL: "javascript:alert(1)"},
		},
	})
	reader.remediations = []v1alpha1.Remediation{failedRemediation("run-1", 5)}

	body := get(t, h, "/remediations/run-1", nil).Body.String()
	mustContain(t, body, "https://grafana.example.com/d/k8s?ns=payments",
		"carry the configured link with this record's namespace")
	mustNotContain(t, body, "javascript:", "render a link the operator refused")
}

// The record's own time is what the window is built from, so two readers
// opening the same page an hour apart get the same link.
func TestLinks_AreStableForATerminalRecord(t *testing.T) {
	rem := failedRemediation("run-1", 30)
	links := []Link{{Name: "G", URL: "https://g.example.com/?from={from}&to={to}"}}

	now := resolveLinks(links, &rem, testNow())
	later := resolveLinks(links, &rem, testNow().Add(time.Hour))

	if now[0].URL != later[0].URL {
		t.Errorf("the same record gave two windows:\n%s\n%s", now[0].URL, later[0].URL)
	}
}
