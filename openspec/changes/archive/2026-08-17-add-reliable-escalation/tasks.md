## 1. The runner

- [x] 1.1 Every escalation step runs, whatever the ones before it did
- [x] 1.2 The escalation is Failed only when every step failed
- [x] 1.3 The remediation plan's stop-on-failure rule is untouched

## 2. The mode

- [x] 2.1 `onFailure.mode`: `all` (default) and `firstSuccess`
- [x] 2.2 Copied onto the Remediation, so the policy at creation is the one used
- [x] 2.3 CRDs regenerated

## 3. Proof

- [x] 3.1 Tests: a fallback page lands when the first channel is down
- [x] 3.2 Tests: `firstSuccess` does not page twice
- [x] 3.3 e2e: a failed first channel does not silence the second
- [x] 3.4 Cookbook, routing guide, architecture and CHANGELOG
