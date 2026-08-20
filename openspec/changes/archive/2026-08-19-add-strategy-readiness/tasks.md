## 1. The controller

- [x] 1.1 `StrategyReconciler` in `internal/engine/strategy.go`, watching
      `RemediationStrategy` and the `Remediation` records that name one
- [x] 1.2 Registered in `cmd/remedik/main.go`, so it is leader-elected
- [x] 1.3 No write when the computed status equals the stored one

## 2. What it says

- [x] 2.1 `Ready=False`, reason `UnknownAction`, naming the step and listing
      the actions this build has — for `steps` and for `onFailure.steps`
- [x] 2.2 `Ready=True` otherwise, including for a disabled strategy
- [x] 2.3 `executionCount`, `lastExecutionTime` and `observedGeneration`
- [x] 2.4 A `Ready` print column on the CRD

## 3. The claims that were not true

- [x] 3.1 `executionCount`'s comment says what a derived counter can promise
- [x] 3.2 `Step.Action`'s comment stops describing admission-time validation
      that does not exist
- [x] 3.3 The `remediation-strategies` spec catches up with what the resource
      actually has: `approval` and `manual` modes, `blastRadius`,
      `giveUpAfter`, `onFailure.steps` and `escalationMode`

## 4. Proof

- [x] 4.1 Unit tests: unknown action in a step, in an escalation step, a
      strategy that becomes valid again, the no-op pass, the counter
- [x] 4.2 e2e: a strategy naming an action this build does not have is not
      Ready, and the message says which
- [x] 4.3 Troubleshooting, cookbook, architecture and CHANGELOG
