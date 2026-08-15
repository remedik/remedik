## Purpose

Define the RemediationStrategy resource: the declarative contract that maps
alerts to remediation behavior — matching, execution mode, guards, steps,
and failure policy.

## ADDED Requirements

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

The engine SHALL NOT start an execution when the same (strategy, target)
pair completed within `guards.cooldown`, or when the strategy has already
started `guards.maxPerHour` executions in the trailing hour. A guard
rejection SHALL be recorded as a Kubernetes event on the strategy with the
rejecting guard named.

#### Scenario: Cooldown blocks re-execution

- **WHEN** an alert re-fires 5 minutes after a completed execution and the strategy cooldown is 15m
- **THEN** no new execution starts and the rejection reason "cooldown" is recorded
