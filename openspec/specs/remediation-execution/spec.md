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
