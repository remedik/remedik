## MODIFIED Requirements

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
