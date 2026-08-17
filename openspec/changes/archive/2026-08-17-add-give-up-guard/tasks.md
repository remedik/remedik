## 1. The guard

- [x] 1.1 `giveUpAfter: {count, within}` on a strategy's guards
- [x] 1.2 Counts completions for (strategy, target) inside the window
- [x] 1.3 Off unless configured

## 2. Telling somebody

- [x] 2.1 A `Remediation` with no steps, Failed, reason GaveUp
- [x] 2.2 It runs the strategy's onFailure.steps
- [x] 2.3 One per trip: further alerts are refused quietly
- [x] 2.4 It never counts toward any guard, on creation or on replay

## 3. Proof

- [x] 3.1 Unit tests for the guard, including the succeeding-remediation case
- [x] 3.2 Tests that the record escalates and does not feed the guards
- [x] 3.3 e2e: the fifth remediation gives up and pages
- [x] 3.4 Cookbook, architecture, chart README and CHANGELOG
