## 1. The resolver

- [x] 1.1 A `Posture` type: a default plus per-namespace overrides, k8s-free and table-tested
- [x] 1.2 Parsing from a flag, rejecting anything that is not `live` or `dryRun`
- [x] 1.3 The sink resolves the posture from the target's namespace, once

## 2. The engine

- [x] 2.1 The reconciler trusts the record's posture and stops OR-ing the global flag
- [x] 2.2 The escalation is told the posture the record actually ran under

## 3. Configuration

- [x] 3.1 `--namespace-posture` on the binary, logged at startup
- [x] 3.2 `namespacePosture` in the chart, with the posture printed in NOTES.txt
- [x] 3.3 `remedik_namespace_posture{namespace,posture}`

## 4. Visibility

- [x] 4.1 The dashboard's badge says "mixed" when overrides exist, and names them
- [x] 4.2 Every record shows the posture it ran under
- [x] 4.3 e2e: a live namespace acts while the default simulates, and the record says which
- [x] 4.4 README, architecture, chart README, CHANGELOG
