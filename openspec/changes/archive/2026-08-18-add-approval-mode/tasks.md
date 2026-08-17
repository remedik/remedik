## 1. The gate

- [x] 1.1 `execution.mode: approval` and `manual`, enum widened
- [x] 1.2 `AwaitingApproval` state; nothing is resolved or planned while waiting
- [x] 1.3 `spec.approval` with a decision, a claimed approver and a note

## 2. The outcomes

- [x] 2.1 Approved: the remediation runs, against the cluster as it is then
- [x] 2.2 Denied: terminal, and no escalation — somebody looked
- [x] 2.3 Timed out: terminal, and escalates, because silence must reach somebody
- [x] 2.4 `manual` never starts from an alert, and the refusal is recorded

## 3. Visible

- [x] 3.1 Waiting records on the overview's attention panel, with time left
- [x] 3.2 A metric for what is waiting — `remedik_remediation_records{state="AwaitingApproval"}`
      needed no new metric: the posture collector reports records by state, so a
      new state is a new series. The Grafana dashboard charts it.

## 4. Proof

- [x] 4.1 Unit tests for each outcome
- [x] 4.2 e2e: a real patch approves; a real timeout escalates
- [x] 4.3 Cookbook, QUICKSTART, invariants, architecture and CHANGELOG
