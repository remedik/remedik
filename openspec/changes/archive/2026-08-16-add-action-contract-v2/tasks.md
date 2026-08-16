## 1. The contract

- [x] 1.1 `action.Result`: summary, kubectl equivalent, structured outputs; `Plan` and `Execute` return it
- [x] 1.2 `action.Verifier`: optional post-condition, read-only, bounded by a timeout parameter
- [x] 1.3 `api/v1alpha1`: `StepStatus.kubectl`, `.outputs`, `.verified`; `make generate manifests`

## 2. The engine

- [x] 2.1 `StepRunner` records the new fields and calls `Verify` after `Execute`, never in dry-run
- [x] 2.2 A failed verify fails the step, with the check's message on the record
- [x] 2.3 Events on the remediated object: `Remediating` before, `Remediated` or `RemediationFailed` after
- [x] 2.4 Target kinds resolved through the manager's RESTMapper; an unaddressable target logs and skips, never fails

## 3. The reference action

- [x] 3.1 `deployment.restart` returns a `Result` with the kubectl equivalent and outputs
- [x] 3.2 `deployment.restart` implements `Verifier`: rollout complete at the observed generation, all replicas ready

## 4. Visibility

- [x] 4.1 Dashboard step timeline shows the kubectl equivalent, the outputs and the verification
- [x] 4.2 Tests: verify success and timeout, events published and addressed, outputs recorded, dry-run does not verify
- [x] 4.3 e2e: after a real restart, the Deployment carries remedik's events and the record carries the verification
- [x] 4.4 Docs: architecture (contract section), CHANGELOG, chart README unchanged (no new RBAC)
