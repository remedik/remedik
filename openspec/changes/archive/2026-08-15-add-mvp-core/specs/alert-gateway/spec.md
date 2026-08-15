## Purpose

Receive alerts from Alertmanager over HTTP, authenticate the sender, and turn
grouped webhook payloads into individual, normalized alert events for the
engine.

## ADDED Requirements

### Requirement: Alertmanager webhook ingestion

The gateway SHALL accept HTTP POST requests in the Alertmanager webhook
format (payload version "4") on a configurable path and SHALL convert each
entry of the payload's `alerts` array into one normalized alert event
containing at minimum: fingerprint, status (`firing` or `resolved`), labels,
annotations, and `startsAt`.

#### Scenario: Grouped payload is split

- **WHEN** Alertmanager posts a webhook payload containing 3 alerts
- **THEN** the gateway produces 3 normalized alert events, each preserving its own labels and fingerprint

### Requirement: Request authentication

The gateway SHALL reject requests that do not present the configured bearer
token with status 401, without processing the request body.

#### Scenario: Missing token

- **WHEN** a request arrives without an Authorization header while a token is configured
- **THEN** the gateway responds 401 and no alert events are produced

#### Scenario: Valid token

- **WHEN** a request presents the configured bearer token
- **THEN** the payload is processed normally

### Requirement: Malformed payload handling

The gateway SHALL respond 400 to syntactically invalid payloads, and SHALL
respond 200 to valid payloads even when no strategy matches, so that
Alertmanager does not retry deliveries that were understood.

#### Scenario: Invalid JSON

- **WHEN** the request body is not valid JSON
- **THEN** the gateway responds 400 and increments an ingestion error metric

#### Scenario: No matching strategy

- **WHEN** a valid alert event matches no RemediationStrategy
- **THEN** the gateway responds 200 and the unmatched event is observable in metrics
