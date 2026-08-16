# action-node-drain Specification

## Purpose

Empty a node, honouring the disruption budgets its workloads declared. The
widest action remedik has.

## Requirements

### Requirement: Cordon, then evict

The `node.drain` action SHALL mark the node unschedulable before evicting
anything — draining without that is a race against the scheduler — and SHALL
remove pods through the Eviction API so that PodDisruptionBudgets are
consulted.

An eviction refused by a budget SHALL be retried until the step's timeout: a
drain is a loop, and "not yet" is the normal answer partway through one.

#### Scenario: The node stops taking work before losing it

- **WHEN** a drain begins on a schedulable node
- **THEN** the node is cordoned first

#### Scenario: A budget refusal is retried, not failed

- **WHEN** a PodDisruptionBudget refuses an eviction and later permits it
- **THEN** the pod is evicted and the step succeeds

### Requirement: A partial drain is a failure

Where the drain cannot finish within the step's timeout, the step SHALL
fail, naming how many pods remain, and the node SHALL stay cordoned.

Half-drained is the worst state to leave a node in: cordoned, missing some
of its work, and nobody knowing whether to continue or reverse. Reporting it
as success would lose capacity that no dashboard accounts for.

#### Scenario: A drain that cannot finish

- **WHEN** a budget refuses for longer than the timeout
- **THEN** the step fails, names the remaining pods, and says the node is still cordoned

### Requirement: What a drain leaves alone

The action SHALL skip pods owned by a DaemonSet, mirror pods, pods that have
already finished, pods with no controller, and pods using an emptyDir —
recording what was skipped and why. The last two SHALL be opt-in rather than
refused outright.

A DaemonSet's controller replaces its pod immediately, so evicting it is a
loop that never ends; a mirror pod cannot be evicted at all; and evicting a
pod nothing owns is deletion rather than remediation.

#### Scenario: A DaemonSet pod is left alone

- **WHEN** the node runs DaemonSet pods
- **THEN** they are skipped, and the record says how many and why

#### Scenario: A dry run names what would move

- **WHEN** the operator is in dry-run
- **THEN** the plan names each pod that would be evicted, and nothing is touched
