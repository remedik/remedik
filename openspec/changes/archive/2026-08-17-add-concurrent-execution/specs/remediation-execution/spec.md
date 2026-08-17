## ADDED Requirements

### Requirement: Remediations for different resources execute concurrently

The reconciler SHALL execute more than one `Remediation` at a time, bounded by
a configured limit.

A single worker meant one slow remediation stalled every other one in the
cluster. The values the CRD already permits put that at fifteen hours in the
worst case, and at half an hour in the ordinary one — a `job.run` that waits
for a pipeline's verdict, retried twice. remedik exists to absorb an alert
storm, which is many alerts about many workloads at once.

A single `Remediation` SHALL still be reconciled by one worker at a time, so
that a record found in `Running` continues to mean the process died.

The steps within one remediation SHALL remain strictly ordered.

#### Scenario: A slow remediation does not stall another

- **WHEN** one remediation is executing a step that takes a long time
- **AND** a remediation for a different resource is created
- **THEN** the second executes without waiting for the first to finish

#### Scenario: One record is never executed twice at once

- **WHEN** a `Remediation` is being reconciled
- **THEN** no second worker reconciles that same record

### Requirement: The concurrency limit is a blast-radius setting

The limit SHALL be configurable, SHALL default to a small fixed number rather
than to a property of the machine, and SHALL be documented as how many
remediations may be changing the cluster at the same moment.

Deriving it from the CPU count would make the number of simultaneous changes to
somebody's cluster a consequence of which node the operator was scheduled on.

A limit below one SHALL be refused when the operator starts, rather than
silently corrected, because a value that does not do what it says is worse than
one that is rejected.

#### Scenario: A nonsensical limit stops the operator

- **WHEN** the concurrency limit is set to zero or a negative number
- **THEN** the operator refuses to start and says why

#### Scenario: The default does not depend on the host

- **WHEN** the operator runs on a machine with many cores
- **THEN** the limit is the configured default, unchanged
