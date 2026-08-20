package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// A hostile string cannot forge a log line.
//
// CodeQL flags eleven places where something an attacker controls reaches a
// log entry — an alert label, a request path, a remote address. The flag is
// correct about the data flow and wrong about the consequence here, and the
// difference is worth pinning rather than arguing: every one of those call
// sites passes the value as a structured attribute, never as part of the
// message, and the operator logs JSON.
//
// So the property that matters is not "untrusted data never reaches a log" —
// it does, and it must, because the labels are the incident. It is that one
// delivery can never become two records, which is what log injection buys an
// attacker: a forged line saying the remediation succeeded, or a fabricated
// error blamed on somebody else.
func TestAHostileValueCannotForgeALogRecord(t *testing.T) {
	hostile := "payments\"}\n{\"time\":\"2026-01-01T00:00:00Z\",\"level\":\"ERROR\"," +
		"\"msg\":\"forged: remediation approved by admin\",\"x\":\""

	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	logger.Info("rejected unauthenticated alert delivery", "namespace", hostile, "path", "/x")

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("one call produced %d records; the value escaped its field:\n%s",
			len(lines), out.String())
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("the record is not valid JSON, so something escaped: %v\n%s", err, lines[0])
	}
	if record["namespace"] != hostile {
		t.Errorf("the value was altered on the way in:\n got %q\nwant %q",
			record["namespace"], hostile)
	}
	if record["msg"] != "rejected unauthenticated alert delivery" {
		t.Errorf("the message became %q", record["msg"])
	}
}

// And the same for the text handler, which is what somebody gets if they set
// a plain-text logger by hand: it quotes what needs quoting.
func TestAHostileValueIsQuotedInTextOutput(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&out, nil))
	logger.Info("gateway", "path", "/x\nmsg=forged")

	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 1 {
		t.Fatalf("a newline in a value split the record in two:\n%s", out.String())
	}
}
