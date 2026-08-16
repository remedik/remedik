## 1. workload.restart

- [x] 1.1 Generalise the restart action to Deployment, StatefulSet and DaemonSet, keeping `deployment.restart` pinned to Deployments
- [x] 1.2 Resolve the kind from whichever workload label the alert carries; explicit `kind`/`name` parameters win
- [x] 1.3 Verify the rollout per kind: observed generation, updated, available and ready

## 2. pod.delete

- [x] 2.1 Evict through the Eviction API; a 429 fails the step naming the PodDisruptionBudget
- [x] 2.2 Refuse a pod with no controller owner unless `requireOwner: "false"`
- [x] 2.3 Verify the pod is gone, or replaced by one with a different UID

## 3. job.delete

- [x] 3.1 Delete the Job with background propagation so its pods go too
- [x] 3.2 Verify the Job is gone

## 4. Chart and packaging

- [x] 4.1 `charts/remedik/action-rbac.yaml`: one reviewable table of what each action may do
- [x] 4.2 `rbac.yaml` grants an action's rules only when that action is enabled
- [x] 4.3 `actions.*` values for the three new actions, off by default except the restart
- [x] 4.4 `hack/rbac-unchanged.sh` extended: enabling an action grants exactly its rules and nothing else

## 5. Tests and docs

- [x] 5.1 Unit tests per action, including every refusal
- [x] 5.2 e2e: a StatefulSet is restarted, a pod is evicted, a bare pod is refused
- [x] 5.3 Cookbook entries for the three new actions
- [x] 5.4 Docs: architecture action list, CHANGELOG, chart README regenerated
