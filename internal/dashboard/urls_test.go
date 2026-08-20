package dashboard

import (
	"net/http"
	"testing"

	"github.com/remedik/remedik/api/v1alpha1"
)

// Every URL these pages can be asked for, including the ones nobody meant to
// ask for.
//
// The dashboard's parameters all arrive from a query string, and its own
// history says what that costs: an empty filter result once made a row loop
// start at index -1, and the reader got a closed connection on the page they
// opened because something was already wrong. The handler turns a panic into
// a 500 now, which is why this test can be written as "nothing here answers
// 5xx" rather than as a list of things not to do.
//
// A 404 is a legitimate answer — an unknown record, an unknown path. A 500 is
// not: every one of these is a URL somebody could paste into an incident
// channel, and the answer to a mistyped parameter is a wide page, never a
// stack trace.
func TestNoURLShapeCanBreakAPage(t *testing.T) {
	h, reader := newHandler(t, Config{})
	reader.strategies = []v1alpha1.RemediationStrategy{
		enabledStrategy(), disabledStrategy(), approvalStrategy(),
	}
	reader.remediations = []v1alpha1.Remediation{
		succeededRemediation("ok-1", 5),
		failedRemediation("bad-1", 20),
		simulatedRemediation("sim-1", "deployment/payments/api", 30),
		pendingRemediation("new-1", 1),
		awaitingRemediation("waiting-1", 2, 9),
		awaitingRemediation("expired-1", 40, 0),
		awaitingRemediation("deadlineless-1", 3, -1),
	}

	paths := []string{
		// The pages themselves, in both spellings where they have two.
		"/", "/remediations", "/remediations/", "/namespaces", "/namespaces/",
		"/approvals", "/approvals/", "/strategies", "/palette",

		// Windows, orders and pages, including the ones that are not.
		"/?range=7d", "/?range=", "/?range=nonsense", "/?range=7d&range=24h",
		"/remediations?page=0", "/remediations?page=-5", "/remediations?page=99999",
		"/remediations?page=abc", "/remediations?page=",
		"/remediations?sort=took&dir=desc", "/remediations?sort=&dir=",
		"/remediations?sort=nonsense&dir=sideways", "/remediations?dir=desc",

		// Every clause at once, and every clause empty.
		"/remediations?namespace=payments&strategy=pod-crashloop&state=Failed" +
			"&escalation=none&target=deployment%2Fpayments%2Fapi&alert=KubePodCrashLooping" +
			"&sort=state&dir=asc&page=2",
		"/remediations?namespace=&strategy=&state=&escalation=&target=&alert=",

		// Values that match nothing, and values that are not text anybody meant.
		"/remediations?namespace=no-such-namespace", "/remediations?state=Nonsense",
		"/remediations?escalation=maybe", "/remediations?target=%2F%2F%2F",
		"/remediations?alert=%00", "/remediations?namespace=%E2%9C%93",
		"/remediations?namespace=" + longValue(2000),

		// The namespaces page's own filter, and the strategies page's.
		"/namespaces?ns=&posture=&show=", "/namespaces?posture=live&show=unheard",
		"/namespaces?posture=sideways&show=everything", "/namespaces?ns=payments",
		"/strategies?show=notready", "/strategies?show=disabled", "/strategies?show=junk",

		// One record, and one that is not there.
		"/remediations/ok-1", "/remediations/waiting-1", "/remediations/expired-1",
		"/remediations/does-not-exist", "/remediations/%2E%2E%2Fetc%2Fpasswd",
	}

	for _, path := range paths {
		rec := get(t, h, path, nil)
		if rec.Code >= http.StatusInternalServerError {
			t.Errorf("GET %s = %d\n%s", path, rec.Code, rec.Body.String())
		}
	}
}

// And with nothing in the cluster at all, which is every page's first minute.
func TestNoURLShapeCanBreakAnEmptyCluster(t *testing.T) {
	h, _ := newHandler(t, Config{})

	for _, path := range []string{
		"/", "/?range=7d", "/remediations", "/remediations?page=4&sort=took",
		"/namespaces", "/namespaces?show=unheard", "/approvals", "/strategies",
		"/palette",
	} {
		if code := get(t, h, path, nil).Code; code != http.StatusOK {
			t.Errorf("GET %s on an empty cluster = %d, want 200", path, code)
		}
	}
}

func longValue(n int) string {
	value := make([]byte, n)
	for i := range value {
		value[i] = 'a'
	}
	return string(value)
}
