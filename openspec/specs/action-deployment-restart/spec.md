# action-deployment-restart Specification

## Purpose
The first built-in action: safe rollout restart of a Deployment, equivalent
to `kubectl rollout restart`. It establishes the contract every action
follows: validate, plan (dry-run), execute, minimal RBAC.

## Requirements

### Requirement: Rollout restart semantics

The `deployment.restart` action SHALL resolve its target Deployment from the
alert labels `namespace` and `deployment` (overridable via step parameters)
and SHALL trigger a rolling restart by patching the pod template's restart
annotation — never by deleting pods directly.

#### Scenario: Successful restart

- **WHEN** the action executes against an existing Deployment
- **THEN** the Deployment's pod template restart annotation is updated and the step record includes the patched resource version

#### Scenario: Missing target

- **WHEN** the target Deployment does not exist
- **THEN** the step fails with a not-found reason and no other resource is touched

### Requirement: Action-scoped permissions

Enabling `deployment.restart` SHALL require only `get` and `patch` on
`deployments` in the `apps` group; the chart SHALL grant exactly these verbs
for this action, and the action SHALL fail cleanly (recorded in the
Remediation status, without crashing the operator) when permissions are
missing.

#### Scenario: RBAC failure is recorded

- **WHEN** the operator lacks `patch` permission on Deployments
- **THEN** the step fails with a permission reason in the Remediation status and the operator continues serving other work
