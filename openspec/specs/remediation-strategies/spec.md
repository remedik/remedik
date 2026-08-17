# remediation-strategies Specification

## Purpose
Define the RemediationStrategy resource: the declarative contract that maps
alerts to remediation behavior — matching, execution mode, guards, steps,
and failure policy.

## Requirements

### Requirement: Strategy resource schema

The system SHALL provide a cluster-scoped `RemediationStrategy` custom
resource in API group `remedik.dev/v1alpha1` with: `spec.enabled` (default
`true`), `spec.trigger.match` (equality matchers over alert labels, at least
one required), `spec.execution.mode` (only `auto` valid in this version),
`spec.guards` (`cooldown` duration, `maxPerHour` integer), `spec.steps`
(ordered list of action references with parameters, at least one required),
and `spec.onFailure` (`retries` integer, default 0).

#### Scenario: Invalid strategy rejected

- **WHEN** a RemediationStrategy is applied with zero steps or zero trigger matchers
- **THEN** the API server rejects it with a validation error naming the missing field

### Requirement: Alert-to-strategy matching

The engine SHALL select a strategy for an alert event when every matcher in
`spec.trigger.match` equals the corresponding alert label, considering only
strategies with `spec.enabled: true`, and SHALL select at most one strategy
per event: the one with the most matchers, with ties broken deterministically
by lexical name order.

#### Scenario: Most specific strategy wins

- **WHEN** an alert matches strategy A (1 matcher) and strategy B (2 matchers)
- **THEN** strategy B is selected and strategy A is not executed

#### Scenario: Disabled strategy ignored

- **WHEN** the only matching strategy has `enabled: false`
- **THEN** no remediation is created and the skip is observable in metrics

### Requirement: Guard evaluation

Before creating a `Remediation`, the engine SHALL evaluate every configured
guard and SHALL create nothing when one refuses, publishing the refusal as
an event on the strategy so that `kubectl describe remediationstrategy`
answers "why did nothing happen?".

Three guards are supported, all opt-in:

- `cooldown` — the minimum time between one execution completing on a target
  and the next starting on that same target.
- `maxPerHour` — how many executions the strategy may start in the trailing
  hour.
- `blastRadius` — how degraded the workload may already be. `minAvailable`
  refuses while the workload has that many available replicas or fewer;
  `maxUnavailablePercent` refuses while that share of it is already
  unavailable.

`blastRadius` SHALL be evaluated against the workload the target belongs to,
resolving a pod to its controller. Where there is no workload to measure —
a node, or an action that touches nothing — it SHALL allow.

Where the workload cannot be read at all, `blastRadius` SHALL refuse. A
guard that permits an execution when it could not evaluate its own condition
is not a guard.

#### Scenario: The last available replica is protected

- **WHEN** a strategy sets `minAvailable: 1` and the workload has one available replica
- **THEN** the execution is refused, and the refusal names the workload and its availability

#### Scenario: An already-degraded workload is left alone

- **WHEN** a strategy sets `maxUnavailablePercent: 25` and 2 of 4 replicas are unavailable
- **THEN** the execution is refused

#### Scenario: A healthy workload is remediated

- **WHEN** every replica is available and the limits are not reached
- **THEN** the guard allows the execution

#### Scenario: There is nothing to measure

- **WHEN** the target is a node, or the action acts on nothing in the cluster
- **THEN** `blastRadius` allows, because it has no workload to evaluate

#### Scenario: A workload that cannot be read is not remediated

- **WHEN** the guard cannot read the workload — no permission, or an API error
- **THEN** the execution is refused, and the reason names the failure

#### Scenario: Guard rejection is visible

- **WHEN** a guard refuses an execution
- **THEN** an event on the strategy records which guard refused it and why

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
