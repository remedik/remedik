package alert_test

import (
	"testing"

	"github.com/remedik/remedik/internal/alert"
)

// The one place remedik reads bytes it did not write.
//
// Everything else in this operator starts from a Kubernetes object the API
// server has already validated against a schema. This starts from an HTTP body
// posted by whatever reached the gateway's port, and the gateway answers 200 to
// anything it understood — so a payload that makes this function behave badly
// is a payload anybody can send.
//
// The properties below are the ones the function's own documentation promises,
// which is what makes them worth asserting rather than the output itself: a
// fuzzer cannot know what a correct Alert looks like, and it can know that a
// refusal must return nothing.
func FuzzParseWebhook(f *testing.F) {
	f.Add([]byte(`{"version":"4","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"X"}}]}`))
	f.Add([]byte(`{"version":"4","alerts":[{"status":"resolved","labels":{"alertname":"X","namespace":"p"},"annotations":{"summary":"s"},"startsAt":"2026-08-20T10:00:00Z","fingerprint":"abc"}]}`))
	f.Add([]byte(`{"alerts":[]}`))
	f.Add([]byte(`{"version":"3","alerts":[]}`))
	f.Add([]byte(`{"truncatedAlerts":7,"alerts":[{"status":"firing","labels":{"alertname":"X"}}]}`))
	f.Add([]byte(`{"alerts":[{"status":"nonsense","labels":{"a":"b"}}]}`))
	f.Add([]byte(`{`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, payload []byte) {
		result, err := alert.ParseWebhookBytes(payload)

		if err != nil {
			// "a partially understood delivery is not safe to act on", so a
			// refusal must hand back nothing at all.
			if len(result.Alerts) != 0 {
				t.Fatalf("refused the payload and still returned %d alerts", len(result.Alerts))
			}
			return
		}

		for i, a := range result.Alerts {
			// Both are promised by normalize: an alert with no labels is
			// refused, and a fingerprint is derived when the sender omits one.
			// A remediation keyed on an empty fingerprint would collide with
			// every other alert that has none.
			if len(a.Labels) == 0 {
				t.Fatalf("alert[%d] was accepted with no labels", i)
			}
			if a.Fingerprint == "" {
				t.Fatalf("alert[%d] was accepted with no fingerprint", i)
			}
			if a.Status == "" {
				t.Fatalf("alert[%d] was accepted with no status", i)
			}
		}

		// Alertmanager reports how many it dropped. A negative count is not a
		// number of dropped alerts, and it reaches a metric and a record.
		if result.TruncatedAlerts < 0 {
			t.Fatalf("accepted a negative truncated count: %d", result.TruncatedAlerts)
		}
	})
}
