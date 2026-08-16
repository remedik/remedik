## MODIFIED Requirements

### Requirement: Rollout restart semantics

The `deployment.restart` action SHALL resolve its target Deployment from the
alert labels `namespace` and `deployment` (overridable via step parameters)
and SHALL trigger a rolling restart by patching the pod template's restart
annotation — never by deleting pods directly.

It SHALL report the equivalent `kubectl rollout restart` command, and SHALL
record the replica count and the restart timestamp as structured outputs.

It SHALL verify its own work: after executing, it waits for the rollout to
reach the observed generation with every replica updated, available and
ready, within a bounded timeout, and reports how many replicas are ready. A
rollout that does not complete within the timeout SHALL fail the step.

#### Scenario: Successful restart

- **WHEN** the action executes against an existing Deployment
- **THEN** the Deployment's pod template restart annotation is updated and the step record includes the patched resource version

#### Scenario: The restart is confirmed, not assumed

- **WHEN** the rolling restart completes
- **THEN** the step records that every replica is updated, available and ready — rather than only that the patch was accepted

#### Scenario: A rollout that never completes is a failure

- **WHEN** the new pods do not become ready within the verification timeout
- **THEN** the step fails, naming how many replicas were ready, and the retry budget applies

#### Scenario: Missing target

- **WHEN** the target Deployment does not exist
- **THEN** the step fails with a not-found reason and no other resource is touched
