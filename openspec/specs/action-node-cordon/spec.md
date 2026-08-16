# action-node-cordon Specification

## Purpose

Stop new work landing on a node, and put it back. The safest pair of actions
in the catalogue and the right first response to almost every node alert.

## Requirements

### Requirement: Cordon and uncordon a node

The `node.cordon` action SHALL mark a node unschedulable, and
`node.uncordon` SHALL clear that. They are separate verbs so that a strategy
may be granted one without the other.

Both SHALL be idempotent: a node already in the wanted state is a success,
not a failure. An alert fires repeatedly, and a strategy that failed on the
second firing would be unusable.

#### Scenario: A sick node stops taking work

- **WHEN** `node.cordon` runs against a schedulable node
- **THEN** the node becomes unschedulable, nothing is moved, and no pod restarts

#### Scenario: An already-cordoned node is not a failure

- **WHEN** the node is already unschedulable
- **THEN** the step succeeds, recording that nothing changed

#### Scenario: The node is cluster-scoped

- **WHEN** the target is resolved
- **THEN** it carries no namespace
