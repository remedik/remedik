## 1. The guard

- [x] 1.1 `guards.BlastRadius` config and evaluation, with the health read through an interface
- [x] 1.2 Fails closed when the workload cannot be read; allows when there is nothing to measure
- [x] 1.3 `api/v1alpha1`: `Guards.BlastRadius`; `make generate manifests`

## 2. The reading

- [x] 2.1 An engine-side reader resolving a target to a workload's desired and available replicas
- [x] 2.2 A pod resolves through its controller — ReplicaSet to Deployment, or straight to StatefulSet or DaemonSet
- [x] 2.3 A direct client, not the cache

## 3. Chart and docs

- [x] 3.1 `guards.blastRadius.enabled` grants exactly the reads the guard needs
- [x] 3.2 Tests: every limit, the fail-closed path, the not-applicable path, pod-to-workload resolution
- [x] 3.3 Cookbook, architecture, CHANGELOG, chart README
