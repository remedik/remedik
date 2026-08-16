# action-deployment-rollback Specification

## Purpose

Put the previous version back. The most common cause of a crash loop at 3am
is the deploy at 2:50, and this is what an on-call engineer actually types.

## Requirements

### Requirement: Roll back to a kept revision

The `deployment.rollback` action SHALL return a Deployment's pod template to
that of an earlier revision's ReplicaSet, defaulting to the one before the
current, and SHALL accept an explicit revision.

It SHALL read the revision history the Deployment controller already keeps,
so that remedik and `kubectl rollout undo` agree about what "the previous
revision" means, and SHALL fail when no earlier revision was kept.

#### Scenario: The previous version comes back

- **WHEN** the action runs against a Deployment with an earlier revision kept
- **THEN** its pod template is that of the earlier revision, and the record names both revision numbers

#### Scenario: Nothing to roll back to

- **WHEN** `revisionHistoryLimit` left no earlier ReplicaSet
- **THEN** the step fails saying so, and the Deployment is untouched

#### Scenario: Another Deployment's history is not used

- **WHEN** the namespace holds ReplicaSets belonging to other Deployments
- **THEN** only the target's own revisions are considered

### Requirement: Refuse what GitOps will revert

The action SHALL refuse a Deployment carrying the labels or annotations Argo
CD or Flux set, unless the step overrides it, and the refusal SHALL name the
controller and what to do instead.

A rollback those controllers revert within minutes is worse than none:
remedik records a success, the outage continues, and the incident spends its
time discovering that two systems are fighting.

#### Scenario: A GitOps-managed workload is refused

- **WHEN** the Deployment carries an Argo CD or Flux marker
- **THEN** the step fails naming the controller, and suggests reverting the commit

#### Scenario: The refusal can be overridden deliberately

- **WHEN** the step sets `ignoreGitOps: "true"`
- **THEN** the rollback proceeds

### Requirement: The rollback is verified

The action SHALL wait for the resulting rollout to complete, and SHALL fail
the step when it does not.

#### Scenario: A rollback that does not come up is a failure

- **WHEN** the earlier version does not become ready within the timeout
- **THEN** the step fails, naming how far the rollout got
