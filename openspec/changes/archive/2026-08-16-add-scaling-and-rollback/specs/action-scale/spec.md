## Purpose

Change how much of something runs: replicas on a Deployment, or the ceiling
on an autoscaler that has reached it.

## ADDED Requirements

### Requirement: Bounded scaling

The `deployment.scale` and `hpa.scale` actions SHALL accept either an
absolute count or a relative increase, and a relative increase SHALL require
a maximum. A step that names more than one of them SHALL be refused, because
they mean different things.

An increase with no ceiling is an alert storm with a credit card, and a
default ceiling would be a number invented for somebody else's cluster.

#### Scenario: A relative increase without a ceiling is refused

- **WHEN** a step sets `increaseBy` and no `max`
- **THEN** the step fails saying a relative change needs a ceiling

#### Scenario: The ceiling caps the result

- **WHEN** the increase would exceed `max`
- **THEN** the result is `max`

### Requirement: Never fight an autoscaler

`deployment.scale` SHALL refuse a Deployment a HorizontalPodAutoscaler
targets, and SHALL point at `hpa.scale` instead. Where it cannot determine
whether one exists, it SHALL refuse rather than proceed.

Setting replicas on an autoscaled Deployment is reverted on the autoscaler's
next interval, so the remediation records a success that did not stick.

#### Scenario: An autoscaled Deployment is refused

- **WHEN** an HPA targets the Deployment
- **THEN** the step fails naming the autoscaler and the action that would work

#### Scenario: An unanswerable check refuses

- **WHEN** autoscalers cannot be listed
- **THEN** the step fails rather than scaling without knowing

### Requirement: An autoscaler's ceiling only rises

`hpa.scale` SHALL refuse a target at or below the autoscaler's current
`maxReplicas`: reducing headroom during an incident is not a remediation.

#### Scenario: Lowering is refused

- **WHEN** the step asks for fewer replicas than the current ceiling
- **THEN** the step fails and the autoscaler is untouched

### Requirement: Capacity is verified as available, not requested

`deployment.scale` SHALL wait for the new replicas to become available, and
SHALL fail the step when they do not: replicas that cannot schedule are not
capacity.

#### Scenario: Replicas that cannot schedule fail the step

- **WHEN** the new replicas stay pending past the timeout
- **THEN** the step fails, naming how many became available
