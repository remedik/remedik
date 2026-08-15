## Purpose

Execute a selected strategy as an auditable state machine: one Remediation
resource per execution, sequential steps, retries with backoff, and a global
dry-run mode that simulates instead of acting.

## ADDED Requirements

### Requirement: Remediation record per execution

The engine SHALL create one `Remediation` resource (`remedik.dev/v1alpha1`)
for every accepted alert-strategy match, recording: the triggering alert
(fingerprint and labels), the strategy name, the resolved target, per-step
outcomes with timestamps, and a terminal state of `Succeeded`, `Failed`, or
`Simulated`.

#### Scenario: Audit trail is queryable

- **WHEN** an execution finishes
- **THEN** `kubectl get remediations` shows its strategy, target, state and age, and the step-by-step record is present in the resource status

### Requirement: Sequential step execution with retries

The engine SHALL execute `spec.steps` strictly in order, stopping at the
first failed step; on failure it SHALL retry the strategy up to
`onFailure.retries` times with exponential backoff, and every attempt SHALL
be visible in the Remediation status.

#### Scenario: Retry then succeed

- **WHEN** step 1 fails on the first attempt, `retries` is 1, and the second attempt succeeds
- **THEN** the Remediation ends `Succeeded` and its status shows 2 attempts

### Requirement: Global dry-run

WHEN the operator runs with dry-run enabled (the install default), the engine
SHALL perform all matching and guard evaluation, SHALL NOT invoke any
action's mutating operation, and SHALL record the execution as `Simulated`
with the exact plan it would have run.

#### Scenario: Dry-run produces a simulated record

- **WHEN** an alert matches a strategy while dry-run is enabled
- **THEN** a Remediation ending in `Simulated` exists listing every step that would have executed, and no cluster resource was mutated

### Requirement: Crash-safe state

The engine SHALL persist state transitions in the Remediation resource such
that an operator restart mid-execution results in the in-flight execution
being marked `Failed` with reason `Interrupted`, rather than any mutating
step being silently re-run.

#### Scenario: Restart mid-execution

- **WHEN** the operator restarts after step 1 of 2 has completed
- **THEN** the Remediation is marked `Failed` with reason `Interrupted` and no step executes twice without an explicit retry

### Requirement: Bounded history

The engine SHALL prune terminal Remediation resources beyond a configurable
retention (default: most recent 200 per strategy) so that alert storms cannot
grow storage without bound.

#### Scenario: Old records pruned

- **WHEN** a strategy accumulates more terminal Remediations than the retention limit
- **THEN** the oldest terminal records are deleted and the newest are kept
