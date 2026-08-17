## ADDED Requirements

### Requirement: webhook.call can send an Alertmanager alert

`webhook.call` SHALL accept a `format` parameter naming the shape of the
request body. `format: remedik` is the default and is the existing body,
unchanged.

`format: alertmanager` SHALL send an Alertmanager `POST /api/v2/alerts` body:
a JSON array of one alert with `labels`, `annotations` and `startsAt`.

`format` SHALL be a closed set of named shapes, never a template. A strategy
is read during an incident by somebody who did not write it, and the action
must be able to state what it sends.

An unrecognised `format` SHALL be refused when the step is planned, not when
it is executed, so a dry run reports it.

#### Scenario: The default body is unchanged

- **WHEN** a step sets no `format`
- **THEN** the request body is the same object it was before

#### Scenario: An unknown format fails the dry run

- **WHEN** a step sets `format: slack`
- **THEN** planning the step fails and names the formats that exist
- **AND** no request is made

### Requirement: The raised alert carries the labels that route it

An alert Alertmanager cannot route reaches nobody, so the alert raised by
`format: alertmanager` SHALL carry:

- `alertname`, defaulting to `RemediationFailed`, and never the name of the
  alert that triggered the remediation — that alert is still firing, and
  reusing its name would make Alertmanager treat them as one alert
- `severity`, defaulting to `critical`
- every label of the alert that triggered the remediation, so the routing
  tree that delivered the symptom delivers the failure to the same team
- remedik's own `remedik_*` labels

Annotations SHALL carry the prose a person woken up needs: what failed and
the equivalent `kubectl`.

`startsAt` SHALL be set and `endsAt` SHALL NOT be, so Alertmanager expires
the alert through its own `resolve_timeout` rather than remedik needing to
send a resolve it will never send.

#### Scenario: The original alert's labels are preserved

- **WHEN** the triggering alert carried `team=payments`
- **THEN** the raised alert carries `team=payments`

#### Scenario: The raised alert does not impersonate the original

- **WHEN** the triggering alert was `KubePodCrashLooping`
- **THEN** the raised alert's `alertname` is not `KubePodCrashLooping`

#### Scenario: A label the step sets wins over an inherited one

- **WHEN** the step sets `severity: warning`
- **THEN** the raised alert carries `severity: warning`
