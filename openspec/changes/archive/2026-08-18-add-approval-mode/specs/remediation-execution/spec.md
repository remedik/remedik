## ADDED Requirements

### Requirement: A strategy can require human approval

A strategy SHALL support `execution.mode: approval`. A matching alert SHALL
create a `Remediation` that reaches `AwaitingApproval` and waits.

While waiting, remedik SHALL NOT resolve a target, plan a step or execute
anything. Resolution happens after the decision, so the plan describes the
cluster as it is when somebody approves rather than as it was when the alert
arrived.

`AwaitingApproval` SHALL NOT be a terminal state, and SHALL NOT be `Running`, so
that a record found in `Running` continues to mean the process died.

#### Scenario: Nothing happens until somebody decides

- **WHEN** a strategy in approval mode matches an alert
- **THEN** a record is created in `AwaitingApproval`
- **AND** no step has been planned or executed

#### Scenario: Approving runs it

- **WHEN** the record's approval decision is set to approve
- **THEN** the remediation executes its steps

### Requirement: A decision is recorded on the record

Approval SHALL be expressed by setting a decision on the `Remediation` itself,
so that the decision is an ordinary Kubernetes write: attributable in the
cluster's audit log, expressible from a terminal, a runbook, a GitOps commit or
a bot, and requiring nothing outside the cluster.

The record SHALL carry who claims to have decided, and remedik SHALL NOT present
that claim as verified. remedik cannot authenticate a patch; the cluster's audit
log is the authority on who issued it.

#### Scenario: A denial is terminal and quiet

- **WHEN** the decision is deny
- **THEN** the remediation is terminal, and no escalation runs

#### Scenario: The claimed approver is on the record

- **WHEN** a decision names who made it
- **THEN** the record carries that name, and the audit trail shows it

### Requirement: Silence escalates

When no decision is made within `execution.approvalTimeout`, the remediation
SHALL become terminal with a reason of `ApprovalTimeout` and SHALL run the
strategy's `onFailure.steps`.

The failure mode of a human gate is that nobody looks. A gate that quietly drops
what nobody looked at is worse than having no gate, because it converts an alert
into silence.

#### Scenario: Nobody looks

- **WHEN** the approval timeout passes with no decision
- **THEN** the remediation fails as `ApprovalTimeout` and the escalation runs

#### Scenario: Waiting is visible before it times out

- **WHEN** records are awaiting approval
- **THEN** the dashboard shows them as needing attention, with how long is left

### Requirement: A manual strategy never starts from an alert

A strategy SHALL support `execution.mode: manual`, which SHALL NOT create a
remediation in response to an alert, and SHALL record the refusal where a guard
refusal is recorded.

#### Scenario: An alert for a manual strategy does nothing

- **WHEN** an alert matches a strategy in manual mode
- **THEN** no `Remediation` is created
- **AND** the refusal is published on the strategy, so "why did nothing happen"
  has an answer in the usual place
