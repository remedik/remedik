## ADDED Requirements

### Requirement: A strategy reports whether it is usable

The system SHALL maintain a `Ready` condition on every `RemediationStrategy`,
reporting whether the strategy could run if an alert matched it.

`Ready` SHALL be false with reason `UnknownAction` when any step, in
`spec.steps` or in `spec.onFailure.steps`, names an action this build of
remedik does not have — whether because the name is wrong or because the
action is not enabled in the chart, which are the same fact from the
strategy's point of view. The message SHALL name the offending step and list
the actions that are available, because the reader of that message is deciding
between fixing a typo and enabling a feature.

`Ready` SHALL be true otherwise, including when `spec.enabled` is false: the
condition reports whether the strategy *could* run, not whether it will.

The condition SHALL NOT gate execution. A strategy that is not Ready still
matches alerts and still produces records, which fail at their first step
exactly as they do today. The registry remains the only authority on what
remedik can run; the condition exists so that the answer arrives at
`kubectl apply` time rather than during an incident.

#### Scenario: A typo is reported before it matters

- **WHEN** a strategy names an action that does not exist
- **THEN** its `Ready` condition is false with reason `UnknownAction`
- **AND** the message names the step and lists the available actions

#### Scenario: An escalation that could never page anybody

- **WHEN** the unknown action is in `onFailure.steps`
- **THEN** the strategy is not Ready, and the message says the step was an
  escalation step

#### Scenario: A disabled strategy is still checked

- **WHEN** a valid strategy has `enabled: false`
- **THEN** it is Ready, and `kubectl get` shows it as not enabled

#### Scenario: Fixing it clears the condition

- **WHEN** the action name is corrected
- **THEN** the condition becomes true without an operator restart

### Requirement: A strategy reports whether it is being used

The system SHALL report, on each `RemediationStrategy`, how many `Remediation`
records it has produced that the cluster still holds, when the newest of them
was created, and the `metadata.generation` the status reflects. These SHALL be
visible as print columns, so that `kubectl get remediationstrategies` answers
"has this ever fired?" without a second query.

The count is derived from the records, so record retention SHALL be allowed to
lower it. It is a coarse counter for humans; `remedik_remediations_total`
remains the source for rates.

The system SHALL NOT write this status when nothing about it has changed,
because a status write is a watch event and a controller that writes on every
pass never settles.

#### Scenario: A strategy that has fired

- **WHEN** a strategy has produced three records
- **THEN** its status reports three, and the newest record's timestamp

#### Scenario: Nothing changed

- **WHEN** the strategy is reconciled again with no new records
- **THEN** no write is made

## MODIFIED Requirements

### Requirement: Strategy resource schema

The system SHALL provide a cluster-scoped `RemediationStrategy` custom
resource in API group `remedik.dev/v1alpha1` with: `spec.enabled` (default
`true`), `spec.trigger.match` (equality matchers over alert labels, at least
one required), `spec.execution` (`mode` — `auto` (default), `approval` or
`manual` — and `approvalTimeout`), `spec.guards` (`cooldown` duration,
`maxPerHour` integer, `blastRadius`, `giveUpAfter`), `spec.steps` (ordered
list of action references with parameters, at least one required), and
`spec.onFailure` (`retries` integer, default 0; `steps`, the escalation; and
`mode`, `all` (default) or `firstSuccess`).

The resource SHALL carry a status reporting whether it is usable and whether
it is being used, and that status SHALL be the resource's own — never a
summary of any single execution.

#### Scenario: Invalid strategy rejected

- **WHEN** a RemediationStrategy is applied with zero steps or zero trigger matchers
- **THEN** the API server rejects it with a validation error naming the missing field

#### Scenario: An unimplemented execution mode is rejected

- **WHEN** a strategy is applied with an `execution.mode` this build does not implement
- **THEN** the API server rejects it, rather than accepting a manifest that
  asks for a gate and running without one
