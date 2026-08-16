## 1. The API

- [x] 1.1 `OnFailure.Steps`: a plan that runs when the remediation has failed for good
- [x] 1.2 `RemediationStatus.Escalation`: its own step records, kept apart from the remediation's
- [x] 1.3 `make generate manifests`

## 2. The engine

- [x] 2.1 Run the escalation plan after retries are exhausted, never per attempt
- [x] 2.2 Run it in dry-run as well, and tell it that the run was simulated
- [x] 2.3 A failed escalation is recorded and changes no outcome
- [x] 2.4 The terminal state stays Failed

## 3. Visibility

- [x] 3.1 The dashboard shows the escalation, separately from the steps
- [x] 3.2 A metric for escalations, by strategy and outcome
- [x] 3.3 Tests: escalation fires once after retries, not per attempt; dry-run escalates; a failed escalation does not change the state
- [x] 3.4 e2e: a failing remediation escalates to a webhook, and the record shows both
- [x] 3.5 Cookbook entry for the full flow, architecture, CHANGELOG
