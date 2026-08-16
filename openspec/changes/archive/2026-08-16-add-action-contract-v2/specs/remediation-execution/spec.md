## MODIFIED Requirements

### Requirement: Sequential step execution with retries

The engine SHALL execute `spec.steps` strictly in order, stopping at the
first failed step; on failure it SHALL retry the strategy up to
`spec.retries` additional attempts with exponential backoff, and SHALL
record every step's outcome including the ones that never ran.

Each recorded step SHALL carry, in addition to its phase and timings: the
one-line summary of what was done or would be done, the equivalent command a
human would have typed, and any structured outputs the action produced.

An action MAY implement a post-condition check. When it does, the engine
SHALL call it after the step executes, SHALL record its result on the step,
and SHALL treat a failed check as a failed step. The check SHALL NOT be
called in dry-run, where nothing was executed for it to verify.

#### Scenario: A step says what a human would have typed

- **WHEN** a step completes
- **THEN** its record carries the equivalent kubectl command, so the change is reviewable by someone who has not read remedik's source

#### Scenario: A remediation that did not work is not recorded as success

- **WHEN** an action executes without error but its post-condition check fails
- **THEN** the step is Failed, the check's message is recorded, and the retry budget applies as it would to any other failure

#### Scenario: Dry-run does not verify

- **WHEN** the operator is in dry-run
- **THEN** no post-condition check runs, because nothing was executed

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
