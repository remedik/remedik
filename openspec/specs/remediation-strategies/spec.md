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
