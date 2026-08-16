# remediation-execution Specification

## Purpose
Execute a selected strategy as an auditable state machine: one Remediation
resource per execution, sequential steps, retries with backoff, and a global
dry-run mode that simulates instead of acting.

## Requirements

### Requirement: Remediation record per execution

The engine SHALL create one `Remediation` resource (`remedik.dev/v1alpha1`)
for every accepted alert-strategy match, recording: the triggering alert
(fingerprint and labels), the strategy name, the resolved target, per-step
outcomes with timestamps, and a terminal state of `Succeeded`, `Failed`, or
`Simulated`.

The engine SHALL also publish Kubernetes events on the object being
remediated: one when a step begins, and one when it ends, naming the
remediation and the strategy responsible. Failing to publish an event SHALL
NOT fail the remediation.

#### Scenario: The remediated object explains itself

- **WHEN** remedik restarts a Deployment
- **THEN** `kubectl describe deployment <name>` shows events naming remedik, the strategy and the Remediation record, without the reader having to know remedik exists

#### Scenario: An unaddressable target does not break a remediation

- **WHEN** the target's kind cannot be resolved to a Kubernetes API kind
- **THEN** the events are skipped and logged, and the remediation proceeds

#### Scenario: Audit trail is queryable

- **WHEN** an execution finishes
- **THEN** `kubectl get remediations` shows its strategy, target, state and age, and the step-by-step record is present in the resource status

### Requirement: Sequential step execution with retries

The engine SHALL execute `spec.steps` strictly in order, stopping at the
first failed step; on failure it SHALL retry the strategy up to
`onFailure.retries` times with exponential backoff, and every attempt SHALL
be visible in the Remediation status.

Each recorded step SHALL carry, in addition to its phase and timings: the
object it acted on, the one-line summary of what was done or would be done,
the equivalent command a human would have typed, and any structured outputs
the action produced.

Actions SHALL receive, alongside the target and the step's parameters, the
triggering alert's labels and the identity of the remediation and strategy
responsible. An action that hands the incident to something outside the
cluster cannot do its job without them.

An action MAY implement a post-condition check. When it does, the engine
SHALL call it after the step executes, SHALL record its result on the step,
and SHALL treat a failed check as a failed step. The check SHALL NOT be
called in dry-run, where nothing was executed for it to verify.

#### Scenario: Retry then succeed

- **WHEN** step 1 fails on the first attempt, `retries` is 1, and the second attempt succeeds
- **THEN** the Remediation ends `Succeeded` and its status shows 2 attempts

#### Scenario: An action can name where the work came from

- **WHEN** a step hands the incident to something outside the cluster
- **THEN** it has the alert's labels and the names of the remediation and strategy to send with it

#### Scenario: A step says what a human would have typed

- **WHEN** a step completes
- **THEN** its record carries the equivalent kubectl command, so the change is reviewable by someone who has not read remedik's source

#### Scenario: A remediation that did not work is not recorded as success

- **WHEN** an action executes without error but its post-condition check fails
- **THEN** the step is Failed, the check's message is recorded, and the retry budget applies as it would to any other failure

#### Scenario: Dry-run does not verify

- **WHEN** the operator is in dry-run
- **THEN** no post-condition check runs, because nothing was executed

#### Scenario: An action that touches no object records no target

- **WHEN** a step acts on nothing in the cluster
- **THEN** its record carries no target, rather than a placeholder nobody can look up

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
