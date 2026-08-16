## 1. deployment.rollback

- [x] 1.1 Find the target revision's ReplicaSet and put its pod template back
- [x] 1.2 Refuse a workload owned by Argo CD or Flux unless the step overrides it
- [x] 1.3 Verify the rollout completes; record the revision rolled back to

## 2. deployment.scale and hpa.scale

- [x] 2.1 `replicas` or `increaseBy` with a required maximum
- [x] 2.2 `deployment.scale` refuses a workload an HPA owns
- [x] 2.3 `hpa.scale` raises maxReplicas, bounded the same way
- [x] 2.4 Verify the new count is available, not merely requested

## 3. Chart and docs

- [x] 3.1 Three action keys, off by default, with their rules in the RBAC table
- [x] 3.2 Tests, including every refusal
- [x] 3.3 Cookbook, architecture, CHANGELOG, chart README
