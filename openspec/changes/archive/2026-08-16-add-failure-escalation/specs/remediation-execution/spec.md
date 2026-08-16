## ADDED Requirements

### Requirement: Escalation when a remediation fails for good

The engine SHALL run `spec.onFailure.steps` after a remediation has failed and
its retry budget is exhausted, and SHALL NOT run them between retries. The
plan SHALL be copied onto the `Remediation` at creation time, like `steps` and
`retries`, so an execution already under way keeps the behaviour it started
with.

Escalation steps are ordinary actions: they are resolved through the same
registry, gated by the same RBAC, and recorded with the same per-step detail.

The engine SHALL record the outcome in `status.escalation`, separate from
`status.steps`, with a phase of `Succeeded` or `Failed`, the per-step record,
and a message when it failed.

#### Scenario: The retry budget is spent before anybody is paged

- **WHEN** a strategy allows two retries and every attempt fails
- **THEN** the remediation runs three times and the escalation runs exactly once, after the third

#### Scenario: A successful remediation escalates nothing

- **WHEN** the steps succeed
- **THEN** `status.escalation` is absent and no escalation step runs

#### Scenario: The escalation is kept apart from the remediation's own steps

- **WHEN** an escalation has run
- **THEN** `status.steps` contains only the remediation's steps, and the escalation's appear under `status.escalation.steps`

### Requirement: Escalation cannot change the outcome

A remediation that escalated SHALL remain `Failed`. The engine SHALL NOT
change `status.reason` or `status.message` to describe the escalation, and a
failed escalation SHALL NOT produce a reconcile error, a requeue, or a further
attempt at the remediation.

The engine SHALL bound the escalation with its own deadline, so an
unreachable endpoint cannot hold the execution open indefinitely.

#### Scenario: A page that could not be sent is recorded, not retried

- **WHEN** the escalation's webhook returns 503
- **THEN** the remediation stays `Failed` with its own reason, `status.escalation.phase` is `Failed` with the endpoint's error, and nothing is retried

#### Scenario: Escalating is not succeeding

- **WHEN** a remediation fails and its escalation succeeds
- **THEN** the terminal state is `Failed`

### Requirement: Escalation runs during a dry run

The engine SHALL execute escalation steps for real even when the remediation
was simulated, and SHALL NOT call their `Plan` path. This is the only
exception to dry-run in remedik, and exists so a trial proves the escalation
path before it is needed.

The engine SHALL tell the escalation which it was, by setting these labels on
the context it hands the steps, overwriting any alert label of the same name:

| Label | Value |
| --- | --- |
| `remedik_remediation` | the record's name |
| `remedik_strategy` | the strategy that matched |
| `remedik_target` | the object, as `kind/namespace/name` |
| `remedik_reason` | the machine-readable cause |
| `remedik_message` | the human-readable detail |
| `remedik_attempts` | how many attempts were made |
| `remedik_dry_run` | `"true"` when nothing was actually changed |

#### Scenario: A trial proves the escalation path

- **WHEN** the operator is in dry-run and a simulated remediation fails
- **THEN** the escalation steps are executed rather than planned, and are told `remedik_dry_run="true"`

#### Scenario: An alert cannot lie to the escalation

- **WHEN** the triggering alert carries a label named `remedik_reason`
- **THEN** the escalation receives remedik's value, not the alert's

### Requirement: Escalation is visible without reading YAML

The dashboard SHALL show the escalation as its own section of a remediation's
page, stating whether anybody was told before listing which steps ran. A
remediation that failed with no escalation declared SHALL say so explicitly
rather than showing nothing.

The operator SHALL expose `remedik_escalations_total{strategy,outcome}`.

#### Scenario: The silent failure is named

- **WHEN** a remediation failed and its strategy declares no `onFailure.steps`
- **THEN** its page says no alert went anywhere, and names the field that would change that

#### Scenario: A failed page does not look calm

- **WHEN** an escalation failed
- **THEN** the page states that nobody may know, in the same tone it uses for a failed remediation
