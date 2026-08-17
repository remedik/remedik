## 1. The option

- [x] 1.1 `MaxConcurrentReconciles` on the controller, from a flag
- [x] 1.2 A chart value, defaulting to 4, documented as blast radius
- [x] 1.3 A value below 1 is refused at startup rather than silently corrected

## 2. Proof

- [x] 2.1 A test that a slow remediation does not block another
- [x] 2.2 e2e: two remediations for different workloads overlap
- [x] 2.3 Architecture, chart README and CHANGELOG
