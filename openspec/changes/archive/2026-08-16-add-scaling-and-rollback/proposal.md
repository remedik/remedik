## Why

The most common cause of a crash loop at 3am is the deploy at 2:50.
`workload.restart` restarts the broken version; nothing in remedik can
put the working one back.

`kubectl rollout undo` is the command an on-call engineer actually types,
and it is the single highest-value action left unbuilt. It is also the most
likely to surprise somebody, for a reason that has nothing to do with
Kubernetes: in a GitOps cluster, Argo CD or Flux will revert the rollback
within minutes, and the person watching will see remedik "do nothing" twice.

Two smaller gaps sit beside it. `KubeHpaMaxedOut` fires when an autoscaler
is pinned at its ceiling and still under pressure, and the only mechanical
response is to raise the ceiling. And a workload that is simply short of
capacity needs replicas, not a restart.

## What Changes

- **`deployment.rollback`** — what `kubectl rollout undo` does: find the
  previous revision's ReplicaSet and put its pod template back. It
  **refuses on a workload owned by Argo CD or Flux** unless the step says
  otherwise, because a rollback the GitOps controller undoes is worse than
  no rollback: it is an outage plus a mystery.
- **`deployment.scale`** — set or increase replicas, bounded by a maximum
  the step must state. It **refuses when a HorizontalPodAutoscaler owns the
  workload**, because the two would fight and the autoscaler would win.
- **`hpa.scale`** — raise an autoscaler's `maxReplicas`, bounded the same
  way. The one useful mechanical answer to `KubeHpaMaxedOut`.

All three are in the *careful* tier: they cost money or serve last week's
code when they are wrong. They ship with `blastRadius` already available to
bound them, which is why that guard came first.

## Non-goals

- Scaling down. Every alert that reaches remedik is about something being
  unhealthy, and the answer to that is never "run less of it". A step may
  set an absolute `replicas` lower than the current count, deliberately and
  visibly, but there is no "reduce by" verb inviting it.
- Rolling back anything but a Deployment. StatefulSets and DaemonSets keep
  revision history too, but their rollbacks are ordered, slow and much
  harder to undo. They deserve their own change and their own argument.
- Editing what GitOps manages. The refusal is the feature; an override
  exists for the person who knows their cluster better than this chart does.

## Capabilities

### New Capabilities

- `action-deployment-rollback`
- `action-scale`

## Impact

- `internal/action/workload`: two new files.
- The chart gains `actions.deploymentRollback`, `actions.deploymentScale`
  and `actions.hpaScale`, all off by default, with their rules in the RBAC
  table: `update` on deployments and `list` on replicasets for the rollback;
  `patch` on `deployments/scale` and `list` on horizontalpodautoscalers for
  the scale; `patch` on horizontalpodautoscalers for the autoscaler.
