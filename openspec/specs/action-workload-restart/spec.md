# action-workload-restart Specification

## Purpose

Roll out a restart of any workload controller — Deployment, StatefulSet or
DaemonSet — the way `kubectl rollout restart` does, without ever deleting
pods.

## Requirements

### Requirement: Restart any workload kind

The `workload.restart` action SHALL restart Deployments, StatefulSets and
DaemonSets by patching the pod template's restart annotation, never by
deleting pods, so that the controller honours `maxUnavailable`, readiness
probes and PodDisruptionBudgets.

It SHALL resolve the kind and name from whichever of the `deployment`,
`statefulset` or `daemonset` labels the alert carries, and SHALL accept
explicit `kind` and `name` parameters that take precedence.

`deployment.restart` SHALL remain available with its existing behaviour, so
that strategies written against it keep working.

#### Scenario: A StatefulSet is restarted

- **WHEN** an alert carries a `statefulset` label and the step is `workload.restart`
- **THEN** the StatefulSet's pod template annotation is patched and no pod is deleted

#### Scenario: The kind cannot be determined

- **WHEN** the alert carries none of the workload labels and the step names no kind
- **THEN** the step fails, saying which labels it looked for, and nothing is touched

#### Scenario: An owner is never guessed from a pod name

- **WHEN** the alert names only a pod
- **THEN** the step fails rather than deriving a workload name from the pod's name

### Requirement: The restart is verified

The action SHALL wait for the rollout to reach the observed generation with
every replica updated, available and ready, within the step's timeout, and
SHALL fail the step if it does not.

#### Scenario: A stalled rollout fails the step

- **WHEN** the new pods do not become ready within the timeout
- **THEN** the step fails, naming how far the rollout got
