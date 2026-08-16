## Purpose

Delete a failed Job so the CronJob that owns it creates a clean one.

## ADDED Requirements

### Requirement: Delete a Job and its pods

The `job.delete` action SHALL delete a Job, propagating the deletion to its
pods by default so that no orphaned pods are left behind, and SHALL confirm
the Job is gone within the step's timeout.

#### Scenario: A failed Job is removed

- **WHEN** the step runs against an existing Job
- **THEN** the Job and its pods are deleted and the step records that it is gone

#### Scenario: A Job that has already gone

- **WHEN** the Job no longer exists when the step runs
- **THEN** the step fails with a not-found reason rather than reporting success for something it did not do
