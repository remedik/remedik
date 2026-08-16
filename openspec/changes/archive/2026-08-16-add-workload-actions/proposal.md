## Why

remedik has one verb, and it only works on Deployments. Of the alerts
kube-prometheus-stack actually ships, the ones with a mechanical response
need three more before the tool is worth installing:

- `KubeStatefulSetUpdateNotRolledOut` and `KubeDaemonSetRolloutStuck` want
  exactly what `deployment.restart` does, to a different kind. Today a
  strategy for either has nothing to call.
- `KubePodNotReady` wants one stuck pod gone so its controller makes a new
  one. Restarting the whole workload to fix one pod is a bigger change than
  the incident called for.
- `KubeJobFailed` wants the failed Job deleted so its CronJob produces a
  clean one.

All three are in the *safe* tier: reversible, scoped to one object, and what
an on-call engineer would do by hand. They need no new trust model, which is
why they come before scaling and rollback.

There is one detail here that most tools get wrong, and it is the reason
`pod.delete` is worth building rather than telling people to write a script:
**deleting a pod ignores PodDisruptionBudgets.** The Eviction API is the only
call that checks them, and it answers 429 when the deletion would breach the
budget. A remediation that takes down the last healthy replica during an
incident is worse than one that refuses and says why.

## What Changes

- **`workload.restart`** — the rolling restart, for Deployments,
  StatefulSets and DaemonSets. `deployment.restart` stays, pinned to
  Deployments, so existing strategies keep working unchanged.
- **`pod.delete`** — evicts one pod **through the Eviction API**, so a
  PodDisruptionBudget can refuse it. Refuses a pod with no controller owner
  by default: nothing would recreate it, and that is deletion rather than
  remediation.
- **`job.delete`** — deletes a Job and its pods, so the CronJob that owns it
  creates a fresh one.
- Each verifies its own work: the rollout completes, the pod is really gone
  and replaced, the Job no longer exists.
- **The chart's RBAC becomes a table.** One file lists the rules each action
  needs; the template grants a rule only for the actions that are enabled.
  Three `if` blocks were reviewable, nine would not be, and their
  reviewability is what invariant 4 rests on.

## Non-goals

- Scaling, rollback and HPA changes. Those are the *careful* tier — bounded
  changes that can cost money or serve last week's code — and they belong
  with the change that gives them their bounds.
- Node actions. A specialism, and higher risk than anything here.
- Retrying an eviction refused by a PodDisruptionBudget. Waiting for a
  budget to allow a disruption is what `onFailure.retries` and the backoff
  already do, at a level where the operator can see it happening.

## Capabilities

### New Capabilities

- `action-workload-restart`
- `action-pod-delete`
- `action-job-delete`

### Modified Capabilities

(none — the existing actions and the engine are unchanged)

## Impact

- `internal/action/workload`: the restart action generalises; two new files.
- The chart gains `actions.workloadRestart`, `actions.podDelete` and
  `actions.jobDelete`, all **off by default except the restart**, and
  `charts/remedik/action-rbac.yaml` becomes the single reviewable list of
  what each action is allowed to do.
- New permissions, each tied to an action that must be enabled to get it:
  `patch` on statefulsets and daemonsets; `get` on pods and `create` on
  `pods/eviction`; `get` and `delete` on jobs.
