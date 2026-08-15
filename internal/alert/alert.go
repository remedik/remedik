// Package alert defines the normalized alert event that flows through
// remedik, plus parsing of the Alertmanager webhook payload.
//
// The package deliberately depends on the standard library only: alerts are
// the input to every remediation decision, so keeping this layer free of
// Kubernetes and HTTP-framework types makes the matching and guard logic
// testable without a cluster.
package alert

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
)

// Status is the lifecycle state of an alert as reported by the sender.
type Status string

const (
	// StatusFiring means the alert condition is currently true.
	StatusFiring Status = "firing"
	// StatusResolved means the alert condition no longer holds.
	StatusResolved Status = "resolved"
)

// LabelAlertName is the conventional Prometheus label carrying the alert's
// name; remediation strategies match on it.
const LabelAlertName = "alertname"

// Alert is one normalized alert event. It is the unit remedik matches
// strategies against: a grouped Alertmanager payload becomes several Alerts.
type Alert struct {
	// Fingerprint identifies the alert series. It comes from the sender
	// when provided, and is otherwise derived deterministically from the
	// labels (see DeriveFingerprint).
	Fingerprint string
	// Status is either StatusFiring or StatusResolved.
	Status Status
	// Labels carry the identifying dimensions of the alert.
	Labels map[string]string
	// Annotations carry human-facing context (summary, description, runbook).
	Annotations map[string]string
	// StartsAt is when the alert began firing.
	StartsAt time.Time
	// EndsAt is when the alert ended; zero when still firing.
	EndsAt time.Time
	// GeneratorURL links back to the rule that produced the alert; optional.
	GeneratorURL string
}

// Name returns the value of the "alertname" label, or "" when absent.
func (a Alert) Name() string { return a.Labels[LabelAlertName] }

// Label returns the value of label k, or "" when absent.
func (a Alert) Label(k string) string { return a.Labels[k] }

// IsFiring reports whether the alert is currently firing.
func (a Alert) IsFiring() bool { return a.Status == StatusFiring }

// String renders a compact, log-friendly identity for the alert.
func (a Alert) String() string {
	name := a.Name()
	if name == "" {
		name = "<unnamed>"
	}
	return fmt.Sprintf("%s[%s] %s", name, a.Status, a.Fingerprint)
}

// validateStatus normalizes and checks an incoming status value.
func validateStatus(s string) (Status, error) {
	switch Status(strings.ToLower(strings.TrimSpace(s))) {
	case StatusFiring:
		return StatusFiring, nil
	case StatusResolved:
		return StatusResolved, nil
	case "":
		return "", fmt.Errorf("status is empty, want %q or %q", StatusFiring, StatusResolved)
	default:
		return "", fmt.Errorf("unknown status %q, want %q or %q", s, StatusFiring, StatusResolved)
	}
}

// DeriveFingerprint computes a stable identifier from a label set. It is
// used only when the sender omits a fingerprint; senders that provide one
// (Alertmanager always does) keep theirs, so identity stays consistent with
// the upstream system.
//
// The value is a 16-character hex digest and is stable across processes and
// releases: label pairs are sorted, then hashed with a length-delimited
// encoding so that {"ab":"c"} and {"a":"bc"} cannot collide.
func DeriveFingerprint(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := fnv.New64a()
	for _, k := range keys {
		// Length prefixes keep the encoding unambiguous. hash.Hash never
		// returns an error from Write, so the result is safe to ignore.
		_, _ = fmt.Fprintf(h, "%d:%s=%d:%s;", len(k), k, len(labels[k]), labels[k])
	}
	return fmt.Sprintf("%016x", h.Sum64())
}
