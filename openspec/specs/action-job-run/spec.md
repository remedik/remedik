# action-job-run Specification

## Purpose

Run somebody else's container as a remediation step, with the incident in its
environment — and bound what that container may do.

## Requirements

### Requirement: Jobs run where remedik runs

The `job.run` and `script.run` actions SHALL create their Job in the
operator's own namespace and nowhere else, so the permission to start a
container stays namespaced.

#### Scenario: The Job is created in the operator's namespace

- **WHEN** a step runs a Job
- **THEN** it is created in the namespace remedik itself runs in

### Requirement: Authority is named, never inherited

The Job SHALL run under a ServiceAccount the step names, defaulting to
`default`, and SHALL be refused if it names remedik's own: a strategy author
must not inherit every permission the operator holds by writing one word.

#### Scenario: remedik's own account is refused

- **WHEN** a step asks to run as remedik's ServiceAccount
- **THEN** the step fails explaining why, and no Job is created

#### Scenario: Forgetting is safe

- **WHEN** a step names no ServiceAccount
- **THEN** the Job runs as `default`, which can do nothing

### Requirement: The command is unambiguous

The command SHALL be given as a JSON array, so that no quoting rules are
invented and no word-splitting can surprise anyone.

#### Scenario: A shell string is refused

- **WHEN** the command is written as a shell string rather than an array
- **THEN** the step fails, saying what the parameter expects

### Requirement: The incident reaches the container

The alert's labels SHALL be passed as environment variables under a prefix,
and whole as JSON, so that a label cannot replace a variable the container
relies on.

#### Scenario: A label cannot shadow the container's environment

- **WHEN** an alert carries a label named PATH
- **THEN** it arrives as a prefixed variable and the container's PATH is untouched

### Requirement: The Job's result is the step's result

Verification SHALL wait for the Job, treat a Job that Kubernetes has stopped
retrying as a failed step, and record its exit code and the tail of its
output.

#### Scenario: A failed Job is a failed remediation

- **WHEN** the Job's pod exits non-zero and Kubernetes stops retrying it
- **THEN** the step fails, with the exit code and the last lines of output on the record

#### Scenario: A retry still to come is not a failure

- **WHEN** a pod has failed but the Job's backoff limit allows another attempt
- **THEN** verification keeps waiting rather than reporting failure

### Requirement: Scripts come from remedik's own namespace

`script.run` SHALL read its ConfigMap from the operator's own namespace only.
Reading one from elsewhere would let anyone with write access to any
namespace have code executed by the operator.

#### Scenario: A missing script is refused before anything runs

- **WHEN** the ConfigMap or its key does not exist
- **THEN** the step fails and no Job is created
