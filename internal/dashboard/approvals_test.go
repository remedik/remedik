package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/remedik/remedik/api/v1alpha1"
)

// The queue is ordered by how soon it empties itself, not by age. Sorted by
// age — which is what the list does — the record with fourteen minutes left
// sits above the one with forty seconds.
func TestApprovals_SoonestDeadlineFirst(t *testing.T) {
	view := buildApprovals([]v1alpha1.Remediation{
		awaitingRemediation("roomy", 1, 14),
		awaitingRemediation("urgent", 12, 1),
		awaitingRemediation("middling", 6, 5),
	}, []v1alpha1.RemediationStrategy{approvalStrategy()}, testNow())

	var order []string
	for _, waiting := range view.Queue {
		order = append(order, waiting.Name)
	}
	want := []string{"urgent", "middling", "roomy"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("queue order = %v, want %v", order, want)
	}

	// And the urgency is on the row, so a queue scanned at speed sorts itself
	// visually the same way.
	if view.Queue[0].Tone != toneFailed {
		t.Errorf("a minute left has tone %q, want it loud", view.Queue[0].Tone)
	}
	if view.Queue[2].Tone != toneWaiting {
		t.Errorf("fourteen minutes left has tone %q, want it quiet", view.Queue[2].Tone)
	}
}

// A deadline in the past is a word, not a negative number: the reconcile that
// fails the record may not have run yet, and "minus four minutes" is not
// something a reader can act on.
func TestApprovals_ExpiredIsNotANegativeNumber(t *testing.T) {
	view := buildApprovals([]v1alpha1.Remediation{
		awaitingRemediation("gone", 30, 0),
		awaitingRemediation("alive", 2, 9),
	}, []v1alpha1.RemediationStrategy{approvalStrategy()}, testNow())

	if view.Expired != 1 {
		t.Errorf("expired = %d, want 1", view.Expired)
	}
	gone := view.Queue[0]
	if gone.Name != "gone" {
		t.Fatalf("first in the queue is %q, want the expired one", gone.Name)
	}
	if !gone.Expired || gone.Left != "" {
		t.Errorf("expired record shows %q left; want no countdown at all", gone.Left)
	}
	if strings.HasPrefix(gone.Left, "-") {
		t.Errorf("countdown is negative: %q", gone.Left)
	}
}

// The engine refuses to hold a record with no deadline — a human gate that
// waits for ever is the one outcome it must not have — so the page says that
// rather than implying there is time.
func TestApprovals_NoDeadlineIsAlreadyOver(t *testing.T) {
	view := buildApprovals([]v1alpha1.Remediation{
		awaitingRemediation("timed", 1, 10),
		awaitingRemediation("deadlineless", 1, -1),
	}, []v1alpha1.RemediationStrategy{approvalStrategy()}, testNow())

	first := view.Queue[0]
	if first.Name != "deadlineless" {
		t.Fatalf("first in the queue is %q, want the one with no deadline", first.Name)
	}
	if !first.NoDeadline || !first.Expired {
		t.Errorf("a record with no deadline reads as waiting: %+v", first)
	}
}

// What approving it does. Approving something whose effect is on another page
// is how a person approves the wrong thing at three in the morning.
func TestApprovals_ShowWhatWouldRunAndHowToDecide(t *testing.T) {
	view := buildApprovals([]v1alpha1.Remediation{awaitingRemediation("one", 1, 10)},
		[]v1alpha1.RemediationStrategy{approvalStrategy()}, testNow())

	waiting := view.Queue[0]
	if len(waiting.Steps) != 1 || waiting.Steps[0].Action != "deployment.restart" {
		t.Errorf("steps = %+v, want the declared plan", waiting.Steps)
	}
	for _, command := range []string{waiting.Approve, waiting.Deny} {
		if !strings.Contains(command, "-n "+testNamespace) || !strings.Contains(command, "one") {
			t.Errorf("command %q does not carry the record's own namespace and name", command)
		}
	}
	if !strings.Contains(waiting.Approve, `"decision":"approve"`) {
		t.Errorf("approve command = %q", waiting.Approve)
	}
	if !strings.Contains(waiting.Deny, `"decision":"deny"`) {
		t.Errorf("deny command = %q", waiting.Deny)
	}
}

// "Nothing is waiting" and "nothing can ever wait, because no strategy asks"
// are the same empty page and two entirely different situations.
func TestApprovals_TwoKindsOfEmpty(t *testing.T) {
	t.Run("no strategy asks", func(t *testing.T) {
		h, reader := newHandler(t, Config{})
		reader.strategies = []v1alpha1.RemediationStrategy{enabledStrategy()}

		body := get(t, h, approvalsPath, nil).Body.String()
		mustContain(t, body, "No strategy asks for approval",
			"say that nothing can ever wait here")
		mustContain(t, body, "execution.mode: approval", "say what would make one")
	})

	t.Run("nothing waiting now", func(t *testing.T) {
		h, reader := newHandler(t, Config{})
		reader.strategies = []v1alpha1.RemediationStrategy{approvalStrategy()}
		reader.remediations = []v1alpha1.Remediation{succeededRemediation("done", 5)}

		body := get(t, h, approvalsPath, nil).Body.String()
		mustContain(t, body, "Nothing is waiting", "say the queue is empty")
		mustContain(t, body, "payments-restart", "name the strategy that fills it")
		// The verb agrees with the count, and so does the clause after it.
		mustContain(t, body, "1 strategy asks for approval", "write the sentence in English")
		mustContain(t, body, "when it matches an alert", "and keep the rest of it agreeing")
		mustNotContain(t, body, "strategy ask for", "print a plural verb after a singular noun")
	})

	t.Run("several strategies ask", func(t *testing.T) {
		h, reader := newHandler(t, Config{})
		second := approvalStrategy()
		second.Name = "checkout-restart"
		reader.strategies = []v1alpha1.RemediationStrategy{approvalStrategy(), second}

		body := get(t, h, approvalsPath, nil).Body.String()
		mustContain(t, body, "2 strategies ask for approval", "count them")
		mustContain(t, body, "when one of them matches", "and say which one fills the queue")
	})
}

// The count rides on the navigation entry, on every page: an approval queue
// that silently accumulates looks exactly like remediation working, and the
// person who could empty it is on whichever page they happened to open.
func TestApprovals_EveryPageCarriesTheCount(t *testing.T) {
	h, reader := newHandler(t, Config{})
	reader.strategies = []v1alpha1.RemediationStrategy{approvalStrategy()}
	reader.remediations = []v1alpha1.Remediation{
		awaitingRemediation("a", 1, 10),
		awaitingRemediation("b", 2, 11),
		succeededRemediation("done", 5),
	}

	for _, path := range []string{"/", "/remediations", "/namespaces", "/strategies", approvalsPath} {
		body := get(t, h, path, nil).Body.String()
		mustContain(t, body, `<span class="nav-count">2</span>`,
			"carry the waiting count on "+path)
	}
}

// The queue is a page like every other one: it reads, and it refuses to be
// anything else.
func TestApprovals_StillCannotWrite(t *testing.T) {
	h, _ := newHandler(t, Config{})

	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, approvalsPath, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", method, approvalsPath, rec.Code)
		}
	}
}

// Both spellings, without a redirect, for the same reason the list serves
// both: every link on every page would otherwise cost one.
func TestApprovals_BothSpellingsOfThePath(t *testing.T) {
	h, _ := newHandler(t, Config{})

	for _, path := range []string{approvalsPath, approvalsPath + "/"} {
		if code := get(t, h, path, nil).Code; code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, code)
		}
	}
}
