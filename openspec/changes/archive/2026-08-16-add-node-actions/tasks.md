## 1. Cordon and uncordon

- [x] 1.1 `node.cordon` sets `spec.unschedulable`; `node.uncordon` clears it
- [x] 1.2 Both are idempotent: a node already in the wanted state is a success, not a failure
- [x] 1.3 Verified by reading the node back

## 2. Drain

- [x] 2.1 Cordon first, then evict every eligible pod through the Eviction API
- [x] 2.2 A 429 is "not yet": retry until the step's timeout
- [x] 2.3 Skip DaemonSet pods, mirror pods and pods with no controller
- [x] 2.4 A drain that does not finish fails the step, naming what is left; the node stays cordoned

## 3. pvc.expand

- [x] 3.1 Refuse unless the StorageClass sets `allowVolumeExpansion`
- [x] 3.2 Never shrink
- [x] 3.3 Verify the claim reports the new capacity

## 4. Chart and docs

- [x] 4.1 Three action keys, off by default, with their rules and their reasoning in the RBAC table
- [x] 4.2 Tests, including every refusal and the retry-on-429 path
- [x] 4.3 Cookbook, architecture, CHANGELOG, chart README
