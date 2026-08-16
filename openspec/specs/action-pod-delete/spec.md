# action-pod-delete Specification

## Purpose

Remove one unhealthy pod so its controller replaces it, without bypassing
the disruption budget its owner agreed to.

## Requirements

### Requirement: Eviction, not deletion

The `pod.delete` action SHALL remove a pod through the Kubernetes Eviction
API, never by deleting the pod object directly.

A deletion refused by a PodDisruptionBudget SHALL fail the step with a
message naming the budget as the cause, and SHALL NOT be retried within the
step: waiting for a budget to allow a disruption is what the strategy's
retry budget and backoff are for, where an operator can see it happening.

#### Scenario: A disruption budget refuses the eviction

- **WHEN** evicting the pod would breach a PodDisruptionBudget
- **THEN** the step fails saying so, and the pod is still running

#### Scenario: A healthy eviction

- **WHEN** the budget permits it
- **THEN** the pod is evicted, honouring its termination grace period

### Requirement: Never delete a pod nothing will replace

The action SHALL refuse a pod with no controller owner unless the step sets
`requireOwner: "false"`, because nothing recreates a bare pod: deleting one
is not remediation.

#### Scenario: A bare pod is refused

- **WHEN** the target pod has no ownerReferences
- **THEN** the step fails explaining that nothing would recreate it, and the pod is untouched

### Requirement: The eviction is verified

The action SHALL confirm the pod is gone, or has been replaced by a pod of
the same name with a different UID, within the step's timeout.

#### Scenario: A pod stuck terminating fails the step

- **WHEN** the pod is still present with its original UID when the timeout expires
- **THEN** the step fails rather than reporting a remediation that did not take effect
