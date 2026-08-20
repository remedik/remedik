package dashboard

import (
	"strings"
	"testing"

	"github.com/remedik/remedik/api/v1alpha1"
)

func labels(line Timeline) []string {
	out := make([]string, 0, len(line.Entries))
	for _, entry := range line.Entries {
		out = append(out, entry.Label)
	}
	return out
}

// The alert, the wait for a person, the attempt and the escalation are four
// sections with timestamps in them. This is the order they happened in.
func TestTimeline_IsTheOrderItHappenedIn(t *testing.T) {
	rem := succeededRemediation("ok-1", 10)
	line := buildTimeline(&rem)

	got := strings.Join(labels(line), " | ")
	for i, want := range []string{"Alert fired", "Recorded", "Started", "Step 1", "Succeeded"} {
		if !strings.Contains(line.Entries[i].Label, want) {
			t.Errorf("entry %d = %q, want it to be %q\nfull order: %s",
				i, line.Entries[i].Label, want, got)
		}
	}

	// The gap since the previous moment is the number somebody reads a
	// timeline for: this alert had been firing five minutes when remedik
	// recorded it.
	if line.Entries[1].Elapsed != "+5m 0s" {
		t.Errorf("gap from firing to recorded = %q, want +5m 0s", line.Entries[1].Elapsed)
	}
	if line.Entries[0].Elapsed != "" {
		t.Errorf("the first entry has a gap of %q, want none", line.Entries[0].Elapsed)
	}
}

// Timestamps have second granularity, so a record that began and ended inside
// one second has no shape to draw. Saying so is the answer to "was it slow".
func TestTimeline_ASubSecondRecordIsNotDrawnAsBars(t *testing.T) {
	rem := succeededRemediation("quick", 10)
	// Everything in the same second, including the alert.
	rem.Spec.Alert.StartsAt = ptr(at(10))

	line := buildTimeline(&rem)

	if !line.Instant {
		t.Error("a record inside one second is not marked instant")
	}
	if line.Total != "" {
		t.Errorf("total = %q, want nothing rather than a rounded zero", line.Total)
	}
	for _, entry := range line.Entries {
		if entry.Bar != 0 {
			t.Errorf("%s drew a bar of %d%% over a sub-second span", entry.Label, entry.Bar)
		}
	}
}

// The API records who approved and what they said, and no time for it. The
// entry carries no timestamp rather than borrowing one from either side.
func TestTimeline_AnApprovalIsInOrderWithNoTimeOfItsOwn(t *testing.T) {
	rem := succeededRemediation("agreed", 10)
	rem.Spec.Approval = &v1alpha1.Approval{
		Decision: v1alpha1.ApprovalApprove, By: "dana", Note: "checked the dashboard",
	}

	line := buildTimeline(&rem)

	var found bool
	for i, entry := range line.Entries {
		if !strings.HasPrefix(entry.Label, "Approved") {
			continue
		}
		found = true
		if entry.At != "" {
			t.Errorf("the approval carries a timestamp %q the API never recorded", entry.At)
		}
		if !strings.Contains(entry.Detail, "dana") {
			t.Errorf("detail = %q, want it to name who decided", entry.Detail)
		}
		// After the record was made and before it ran: nothing is resolved
		// until it is approved.
		if !strings.HasPrefix(line.Entries[i-1].Label, "Recorded") {
			t.Errorf("the approval follows %q, want it to follow the record",
				line.Entries[i-1].Label)
		}
	}
	if !found {
		t.Fatalf("no approval entry: %v", labels(line))
	}
}

// A step that took real time gets a bar; the rest of them do not.
func TestTimeline_ABarOnlyWhereThereIsASecondToDraw(t *testing.T) {
	rem := succeededRemediation("slow", 10)
	rem.Status.CompletedAt = ptr(at(4))
	rem.Status.Steps[0].StartedAt = ptr(at(10))
	rem.Status.Steps[0].CompletedAt = ptr(at(7))

	line := buildTimeline(&rem)

	var drawn int
	for _, entry := range line.Entries {
		if entry.Bar > 0 {
			drawn++
			if !strings.HasPrefix(entry.Label, "Step") {
				t.Errorf("%s drew a bar and has no duration of its own", entry.Label)
			}
		}
	}
	if drawn != 1 {
		t.Errorf("%d entries drew bars, want just the step that took time", drawn)
	}
	if line.Instant {
		t.Error("an eleven-minute record is marked instant")
	}
}

// Once is not a history.
func TestTargetHistory_OnceIsNotAHistory(t *testing.T) {
	records := []v1alpha1.Remediation{succeededRemediation("only", 5)}

	if history := buildTargetHistory(&records[0], records, testNow()); history != nil {
		t.Errorf("a target with one record has a history panel: %+v", history)
	}
}

// A remediation that keeps being needed is a different problem from one that
// failed, and it is invisible from the page of any single record.
func TestTargetHistory_ListsEarlierOutcomesAndMarksTheCurrent(t *testing.T) {
	records := []v1alpha1.Remediation{
		succeededRemediation("run-1", 10),
		succeededRemediation("run-2", 70),
		succeededRemediation("run-3", 130),
		// A different target, which must not appear.
		failedRemediation("elsewhere", 20),
	}

	history := buildTargetHistory(&records[1], records, testNow())
	if history == nil {
		t.Fatal("three records for one target and no history")
	}
	if len(history.Marks) != 3 {
		t.Fatalf("marks = %d, want the three for this target", len(history.Marks))
	}
	if !strings.Contains(history.URL, "target=") {
		t.Errorf("URL = %q, want the target filter", history.URL)
	}

	var current int
	for _, mark := range history.Marks {
		if mark.Current {
			current++
			if mark.Name != "run-2" {
				t.Errorf("marked %q as the current record", mark.Name)
			}
		}
	}
	if current != 1 {
		t.Errorf("%d marks claim to be the record being read", current)
	}
}

// Past the limit the panel counts the rest rather than growing.
func TestTargetHistory_CountsWhatItDoesNotShow(t *testing.T) {
	var records []v1alpha1.Remediation
	for i := range historyLimit + 3 {
		records = append(records, succeededRemediation("run-"+string(rune('a'+i)), (i+1)*10))
	}

	history := buildTargetHistory(&records[0], records, testNow())
	if len(history.Marks) != historyLimit {
		t.Errorf("marks = %d, want %d", len(history.Marks), historyLimit)
	}
	if history.More != 3 {
		t.Errorf("More = %d, want 3", history.More)
	}
}
