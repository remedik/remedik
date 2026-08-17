## ADDED Requirements

### Requirement: A strategy can give up on a target that keeps needing it

A strategy SHALL support a `giveUpAfter` guard with a count and a window.
When remedik has remediated the same target with the same strategy at least
`count` times inside `within`, it SHALL stop remediating that target and
report that repeated remediation is not resolving the problem.

The guard SHALL count every remediation of that target, whatever it concluded.
The case it exists for is remediations that **succeed**: the rollout completes,
the pods come back ready, and the problem returns. Counting only failures would
miss it entirely.

The guard SHALL be scoped to the strategy and the target, so one workload that
keeps breaking cannot stop remediation for the others a strategy protects.

The guard SHALL be off unless configured. No count and window is right for
every workload, and a tool that stops acting on a default nobody chose is worse
than one that keeps going.

#### Scenario: Five successful remediations of one workload

- **WHEN** a strategy with `giveUpAfter: {count: 5, within: 2h}` has remediated
  one Deployment five times in the last two hours, all of them succeeding
- **AND** the alert arrives again
- **THEN** remedik does not remediate it

#### Scenario: One workload does not silence the others

- **WHEN** the guard has tripped for one target
- **AND** an alert arrives for a different target of the same strategy
- **THEN** that remediation proceeds

#### Scenario: The window clears itself

- **WHEN** the target has had no remediation for longer than `within`
- **THEN** remediation resumes with no intervention

### Requirement: Giving up is recorded and escalated

When the guard trips, remedik SHALL create a `Remediation` with no steps, a
terminal state of `Failed` and a reason of `GaveUp`, and SHALL run the
strategy's `onFailure.steps` for it.

Every other guard refuses into an event and a metric, with no record and no
escalation. That is defensible for "not yet" and not for "I have stopped
helping": the state with the least visibility must not be the one where remedik
has withdrawn.

The record's message SHALL say what is true — that remediation has run this
many times without resolving the problem, and that it needs a person.

#### Scenario: The page goes where the strategy's pages go

- **WHEN** the guard trips on a strategy that declares `onFailure.steps`
- **THEN** those steps run, and their outcome is recorded on the give-up record

#### Scenario: One record per trip

- **WHEN** the guard has already produced a give-up record for this target
  inside the window
- **AND** further alerts arrive for it
- **THEN** they are refused without creating another record or paging again

### Requirement: A give-up record never feeds the guards

A `Remediation` whose reason is `GaveUp` SHALL NOT count as a start, a
completion or a cooldown for any guard, when it is created or when guard state
is rebuilt from existing records at startup.

remedik started nothing. A record of a decision that counted as an action would
extend the window that produced it, and would rebuild guard state from
decisions rather than from what was done.

#### Scenario: It does not extend its own window

- **WHEN** a give-up record is created
- **THEN** the count of remediations in the window is unchanged

#### Scenario: It does not survive a restart as a cooldown

- **WHEN** the operator restarts and replays existing records into the guards
- **THEN** give-up records are skipped
