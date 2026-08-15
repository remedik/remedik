package alert

import (
	"strings"
	"testing"
	"time"
)

// groupedPayload is a realistic Alertmanager v4 delivery carrying three
// alerts, as produced when several series fire in the same group.
const groupedPayload = `{
  "version": "4",
  "groupKey": "{}:{alertname=\"KubePodCrashLooping\"}",
  "truncatedAlerts": 0,
  "status": "firing",
  "receiver": "remedik",
  "groupLabels": {"alertname": "KubePodCrashLooping"},
  "commonLabels": {"alertname": "KubePodCrashLooping", "severity": "warning"},
  "commonAnnotations": {},
  "externalURL": "http://alertmanager.example.com",
  "alerts": [
    {
      "status": "firing",
      "labels": {"alertname": "KubePodCrashLooping", "namespace": "payments", "pod": "api-0", "severity": "warning"},
      "annotations": {"summary": "Pod is crash looping"},
      "startsAt": "2026-08-15T09:00:00Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "generatorURL": "http://prometheus.example.com/graph?g0.expr=up",
      "fingerprint": "aaaa000000000001"
    },
    {
      "status": "firing",
      "labels": {"alertname": "KubePodCrashLooping", "namespace": "payments", "pod": "api-1", "severity": "warning"},
      "annotations": {"summary": "Pod is crash looping"},
      "startsAt": "2026-08-15T09:01:00Z",
      "endsAt": "0001-01-01T00:00:00Z",
      "fingerprint": "aaaa000000000002"
    },
    {
      "status": "resolved",
      "labels": {"alertname": "KubePodCrashLooping", "namespace": "checkout", "pod": "web-3", "severity": "warning"},
      "annotations": {},
      "startsAt": "2026-08-15T08:30:00Z",
      "endsAt": "2026-08-15T09:02:00Z",
      "fingerprint": "aaaa000000000003"
    }
  ]
}`

func TestParseWebhook_GroupedPayloadIsSplit(t *testing.T) {
	got, err := ParseWebhookBytes([]byte(groupedPayload))
	if err != nil {
		t.Fatalf("ParseWebhookBytes() error = %v, want nil", err)
	}

	if len(got.Alerts) != 3 {
		t.Fatalf("got %d alerts, want 3", len(got.Alerts))
	}

	// Each alert keeps its own identity, not the group's common labels.
	wantPods := []string{"api-0", "api-1", "web-3"}
	for i, want := range wantPods {
		if pod := got.Alerts[i].Label("pod"); pod != want {
			t.Errorf("alert[%d] pod = %q, want %q", i, pod, want)
		}
	}

	first := got.Alerts[0]
	if first.Name() != "KubePodCrashLooping" {
		t.Errorf("Name() = %q, want KubePodCrashLooping", first.Name())
	}
	if first.Fingerprint != "aaaa000000000001" {
		t.Errorf("Fingerprint = %q, want the sender's value", first.Fingerprint)
	}
	if !first.IsFiring() {
		t.Errorf("IsFiring() = false, want true")
	}
	if want := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC); !first.StartsAt.Equal(want) {
		t.Errorf("StartsAt = %v, want %v", first.StartsAt, want)
	}
	if first.Annotations["summary"] != "Pod is crash looping" {
		t.Errorf("annotation summary = %q", first.Annotations["summary"])
	}

	last := got.Alerts[2]
	if last.Status != StatusResolved {
		t.Errorf("alert[2].Status = %q, want %q", last.Status, StatusResolved)
	}
	if last.IsFiring() {
		t.Errorf("resolved alert reports IsFiring() = true")
	}
}

func TestParseWebhook_Errors(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{
			name:    "invalid json",
			payload: `{"version": "4", "alerts": [`,
			wantErr: "decode webhook payload",
		},
		{
			name:    "empty body",
			payload: ``,
			wantErr: "empty payload",
		},
		{
			name:    "unsupported version",
			payload: `{"version": "5", "alerts": []}`,
			wantErr: `unsupported webhook payload version "5"`,
		},
		{
			name:    "unknown status",
			payload: `{"version": "4", "alerts": [{"status": "pending", "labels": {"alertname": "X"}}]}`,
			wantErr: `unknown status "pending"`,
		},
		{
			name:    "empty status",
			payload: `{"version": "4", "alerts": [{"labels": {"alertname": "X"}}]}`,
			wantErr: "status is empty",
		},
		{
			name:    "no labels",
			payload: `{"version": "4", "alerts": [{"status": "firing", "labels": {}}]}`,
			wantErr: "alert has no labels",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseWebhookBytes([]byte(tc.payload))
			if err == nil {
				t.Fatalf("ParseWebhookBytes() error = nil, want %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
			if len(got.Alerts) != 0 {
				t.Errorf("got %d alerts on error, want none", len(got.Alerts))
			}
		})
	}
}

func TestParseWebhook_Tolerations(t *testing.T) {
	t.Run("missing version is accepted", func(t *testing.T) {
		got, err := ParseWebhookBytes([]byte(
			`{"alerts": [{"status": "firing", "labels": {"alertname": "X"}}]}`))
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if len(got.Alerts) != 1 {
			t.Fatalf("got %d alerts, want 1", len(got.Alerts))
		}
	})

	t.Run("status is case-insensitive", func(t *testing.T) {
		got, err := ParseWebhookBytes([]byte(
			`{"version": "4", "alerts": [{"status": "FIRING", "labels": {"alertname": "X"}}]}`))
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if got.Alerts[0].Status != StatusFiring {
			t.Errorf("Status = %q, want %q", got.Alerts[0].Status, StatusFiring)
		}
	})

	t.Run("no alerts is valid", func(t *testing.T) {
		got, err := ParseWebhookBytes([]byte(`{"version": "4", "alerts": []}`))
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if len(got.Alerts) != 0 {
			t.Errorf("got %d alerts, want 0", len(got.Alerts))
		}
	})

	t.Run("nil annotations become an empty map", func(t *testing.T) {
		got, err := ParseWebhookBytes([]byte(
			`{"version": "4", "alerts": [{"status": "firing", "labels": {"alertname": "X"}}]}`))
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if got.Alerts[0].Annotations == nil {
			t.Error("Annotations = nil, want an empty map so lookups are safe")
		}
	})

	t.Run("truncatedAlerts is surfaced", func(t *testing.T) {
		got, err := ParseWebhookBytes([]byte(
			`{"version": "4", "truncatedAlerts": 7, "alerts": [{"status": "firing", "labels": {"alertname": "X"}}]}`))
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if got.TruncatedAlerts != 7 {
			t.Errorf("TruncatedAlerts = %d, want 7", got.TruncatedAlerts)
		}
	})
}

func TestParseWebhook_DerivesFingerprintWhenAbsent(t *testing.T) {
	payload := `{"version": "4", "alerts": [
		{"status": "firing", "labels": {"alertname": "X", "ns": "a"}},
		{"status": "firing", "labels": {"ns": "a", "alertname": "X"}},
		{"status": "firing", "labels": {"alertname": "X", "ns": "b"}}
	]}`

	got, err := ParseWebhookBytes([]byte(payload))
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if got.Alerts[0].Fingerprint == "" {
		t.Fatal("Fingerprint is empty, want a derived value")
	}
	// Same labels in a different order must produce the same fingerprint.
	if got.Alerts[0].Fingerprint != got.Alerts[1].Fingerprint {
		t.Errorf("fingerprints differ for identical label sets: %q vs %q",
			got.Alerts[0].Fingerprint, got.Alerts[1].Fingerprint)
	}
	// Different labels must not.
	if got.Alerts[0].Fingerprint == got.Alerts[2].Fingerprint {
		t.Errorf("fingerprint %q collides across different label sets",
			got.Alerts[0].Fingerprint)
	}
}

func TestDeriveFingerprint(t *testing.T) {
	t.Run("is stable across calls", func(t *testing.T) {
		labels := map[string]string{"alertname": "X", "pod": "api-0"}
		if a, b := DeriveFingerprint(labels), DeriveFingerprint(labels); a != b {
			t.Errorf("not stable: %q != %q", a, b)
		}
	})

	t.Run("delimits keys and values unambiguously", func(t *testing.T) {
		// Without length prefixes these two would hash identically.
		a := DeriveFingerprint(map[string]string{"ab": "c"})
		b := DeriveFingerprint(map[string]string{"a": "bc"})
		if a == b {
			t.Errorf("collision between {ab:c} and {a:bc}: %q", a)
		}
	})

	t.Run("has a fixed width", func(t *testing.T) {
		if got := DeriveFingerprint(map[string]string{"a": "b"}); len(got) != 16 {
			t.Errorf("len(%q) = %d, want 16", got, len(got))
		}
	})
}

func TestAlert_String(t *testing.T) {
	a := Alert{
		Fingerprint: "abc",
		Status:      StatusFiring,
		Labels:      map[string]string{LabelAlertName: "KubeNodeNotReady"},
	}
	if got, want := a.String(), "KubeNodeNotReady[firing] abc"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	unnamed := Alert{Fingerprint: "def", Status: StatusResolved, Labels: map[string]string{}}
	if got, want := unnamed.String(), "<unnamed>[resolved] def"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseWebhook_DecodedMapsAreDefensiveCopies(t *testing.T) {
	got, err := ParseWebhookBytes([]byte(
		`{"version": "4", "alerts": [{"status": "firing", "labels": {"alertname": "X"}}]}`))
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	got.Alerts[0].Labels["alertname"] = "mutated"

	again, err := ParseWebhookBytes([]byte(
		`{"version": "4", "alerts": [{"status": "firing", "labels": {"alertname": "X"}}]}`))
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if again.Alerts[0].Name() != "X" {
		t.Errorf("second parse returned mutated data: %q", again.Alerts[0].Name())
	}
}
