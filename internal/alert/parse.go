package alert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// SupportedWebhookVersion is the Alertmanager webhook payload version this
// package understands. Payloads declaring a different version are rejected
// rather than parsed on a guess: a future format could change semantics
// silently, and acting on a misread alert is worse than refusing it.
const SupportedWebhookVersion = "4"

// ErrEmptyPayload is returned when the request body contains no bytes.
var ErrEmptyPayload = errors.New("empty payload")

// webhookPayload mirrors the Alertmanager webhook message. Fields remedik
// does not use (receiver, groupKey, externalURL, ...) are intentionally
// omitted; unknown JSON fields are ignored.
type webhookPayload struct {
	Version         string        `json:"version"`
	TruncatedAlerts int           `json:"truncatedAlerts"`
	Alerts          []webhookItem `json:"alerts"`
}

type webhookItem struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// ParseResult carries the alerts decoded from one webhook delivery.
type ParseResult struct {
	// Alerts are the normalized events, one per entry in the payload.
	Alerts []Alert
	// TruncatedAlerts is the number of alerts Alertmanager dropped from
	// this delivery because the group exceeded its size limit. A non-zero
	// value means remediation may be acting on partial information, so
	// callers should surface it.
	TruncatedAlerts int
}

// ParseWebhook decodes an Alertmanager webhook payload into normalized
// alerts.
//
// Every entry in the payload's "alerts" array becomes one Alert, so a
// grouped delivery of three alerts yields three events. An error is
// returned for malformed JSON, an unsupported payload version, or an entry
// that cannot be interpreted (unknown status, no labels); in that case no
// alerts are returned, because a partially understood delivery is not safe
// to act on.
func ParseWebhook(r io.Reader) (ParseResult, error) {
	var payload webhookPayload

	dec := json.NewDecoder(r)
	if err := dec.Decode(&payload); err != nil {
		if errors.Is(err, io.EOF) {
			return ParseResult{}, ErrEmptyPayload
		}
		return ParseResult{}, fmt.Errorf("decode webhook payload: %w", err)
	}

	// An empty version is tolerated (some senders omit it); a different
	// declared version is not.
	if payload.Version != "" && payload.Version != SupportedWebhookVersion {
		return ParseResult{}, fmt.Errorf(
			"unsupported webhook payload version %q, want %q",
			payload.Version, SupportedWebhookVersion)
	}

	alerts := make([]Alert, 0, len(payload.Alerts))
	for i, item := range payload.Alerts {
		a, err := item.normalize()
		if err != nil {
			return ParseResult{}, fmt.Errorf("alert[%d]: %w", i, err)
		}
		alerts = append(alerts, a)
	}

	return ParseResult{Alerts: alerts, TruncatedAlerts: payload.TruncatedAlerts}, nil
}

// ParseWebhookBytes is the []byte convenience form of ParseWebhook.
func ParseWebhookBytes(b []byte) (ParseResult, error) {
	if len(b) == 0 {
		return ParseResult{}, ErrEmptyPayload
	}
	return ParseWebhook(bytes.NewReader(b))
}

func (it webhookItem) normalize() (Alert, error) {
	status, err := validateStatus(it.Status)
	if err != nil {
		return Alert{}, err
	}
	if len(it.Labels) == 0 {
		return Alert{}, errors.New("alert has no labels")
	}

	fingerprint := it.Fingerprint
	if fingerprint == "" {
		fingerprint = DeriveFingerprint(it.Labels)
	}

	return Alert{
		Fingerprint:  fingerprint,
		Status:       status,
		Labels:       copyMap(it.Labels),
		Annotations:  copyMap(it.Annotations),
		StartsAt:     it.StartsAt,
		EndsAt:       it.EndsAt,
		GeneratorURL: it.GeneratorURL,
	}, nil
}

// copyMap returns a defensive copy so that callers cannot mutate the
// decoded payload through a shared reference. A nil input yields a non-nil
// empty map, so label lookups never panic downstream.
func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
