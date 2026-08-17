## ADDED Requirements

### Requirement: Every escalation channel is tried

A failed escalation step SHALL NOT prevent the steps after it from running.

Escalation steps are alternative ways to reach a person, not a sequence where
each acts on the last one's result. Stopping at the first failure makes a
configured fallback a single point of failure, and does so invisibly: every
step succeeds when the path is tested.

The remediation's own plan SHALL keep stopping at its first failed step. That
rule is correct there and is not changed by this.

#### Scenario: A fallback page lands when the first channel is down

- **WHEN** the first escalation step cannot reach its endpoint
- **AND** a second step names a different endpoint
- **THEN** the second step runs and its outcome is recorded

#### Scenario: The remediation plan still stops at a failure

- **WHEN** a remediation's second step fails
- **THEN** its third step is recorded as skipped

### Requirement: The escalation reports whether anybody was told

The escalation SHALL be `Succeeded` when at least one step succeeded, and
`Failed` only when every step failed.

The record's question is whether anybody was told. A channel that failed
beside one that got through is visible as its own step with its own message,
and reporting the whole escalation as failed would raise the "nobody was told"
alarm on a night when the page landed.

#### Scenario: One channel through, one down

- **WHEN** one escalation step fails and another succeeds
- **THEN** the escalation is Succeeded, and the failed step is still recorded
  with its own message

#### Scenario: Every channel down

- **WHEN** every escalation step fails
- **THEN** the escalation is Failed

### Requirement: The escalation mode is declared and recorded

A strategy SHALL be able to declare `onFailure.mode`:

- `all`, the default: every step runs.
- `firstSuccess`: steps run in order until one succeeds; the rest are skipped.

`all` is the default because it is what a working configuration already does —
when every step succeeds, every step runs — so no configuration that works
today changes behaviour.

The mode SHALL be resolved when the `Remediation` is created and written onto
it, so an escalation runs under the policy in force when the remediation
started and the record says which one that was.

#### Scenario: An ordered fallback does not page twice

- **WHEN** `mode: firstSuccess` and the first step succeeds
- **THEN** the second step is recorded as skipped and its endpoint is not called

#### Scenario: A later edit does not change a running remediation's escalation

- **WHEN** the strategy's mode is changed after a `Remediation` was created
- **THEN** that remediation escalates under the mode recorded on it
