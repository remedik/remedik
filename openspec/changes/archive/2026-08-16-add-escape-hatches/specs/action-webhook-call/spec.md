## Purpose

Hand the incident to something outside the cluster: a pipeline, a runbook
service, an internal API. The cheapest way to reach everything remedik will
never implement.

## ADDED Requirements

### Requirement: Post the incident to a configured endpoint

The `webhook.call` action SHALL POST a JSON body naming the remediation, the
strategy, the target, the dry-run posture and the alert's labels to a URL the
step configures. It SHALL accept only `http` and `https` URLs, and only the
methods that submit something.

#### Scenario: The far end learns where this came from

- **WHEN** the call is made
- **THEN** the body names the Remediation record and the strategy, so the receiver can trace it back

#### Scenario: A response that is not a success fails the step

- **WHEN** the endpoint answers anything outside 2xx
- **THEN** the step fails, the response body is recorded, and the retry budget applies

### Requirement: Credentials come from remedik's own namespace

A credential SHALL be read only from a Secret in the operator's own
namespace, never from a namespace named by an alert, and SHALL NOT appear in
the Remediation record or in the equivalent command.

#### Scenario: An alert cannot choose the credential

- **WHEN** the alert's labels name a different namespace
- **THEN** the Secret is still read from the operator's namespace

#### Scenario: The record is safe to read

- **WHEN** a call authenticated with a token completes
- **THEN** neither the outputs nor the recorded command contain the token
