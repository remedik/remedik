## Why

`KubeNodeNotReady`, `KubeNodeUnreachable`, `KubeNodeReadinessFlapping` and
`KubeletTooManyPods` are among the alerts a cluster fires most, and remedik
can do nothing about any of them.

The first response to all four is the same, and it is the safest action in
Kubernetes: stop putting new work on that node. Nothing moves, nothing
restarts, and it is undone with one command. That a tool holding write
access to a cluster cannot do the *reversible* thing, while it can restart
workloads and evict pods, is the wrong shape.

Draining is the harder half, and the reason this change comes last.
`KubePersistentVolumeFillingUp` is here too, because it is the remaining
alert with a purely mechanical answer.

## What Changes

- **`node.cordon`** — mark a node unschedulable. Reversible, moves nothing.
- **`node.uncordon`** — the undo. An automation with no reverse gear does
  not get installed twice.
- **`node.drain`** — cordon, then evict every pod **through the Eviction
  API**, honouring PodDisruptionBudgets and retrying the refusals until the
  step's timeout. A drain that cannot finish is a **failure**, not a partial
  success: half-drained is the worst state to leave a node in, and reporting
  it as done is how a cluster loses capacity nobody accounted for.
- **`pvc.expand`** — raise a PersistentVolumeClaim's request, but only where
  the StorageClass allows expansion. Where it does not, the API accepts the
  patch and silently does nothing, so the action checks first and refuses
  with a message that says why.

## Non-goals

- **Deleting nodes, or resizing node groups.** That is a cloud API with a
  different trust model and different credentials. cluster-api's
  MachineHealthCheck and the medik8s operators do it properly; remedik
  should call them — `webhook.call` and `job.run` exist for exactly that —
  not become them.
- **Rebooting.** Same argument, plus kured already exists.
- **Shrinking a volume.** Kubernetes cannot, and neither can this.
- **Bounding a drain by cluster capacity.** "How much of the cluster may be
  unavailable at once" is a real guard and a different one from
  `blastRadius`, which measures a workload. Until it exists, a drain is
  bounded by PDB-honouring eviction, by its own timeout, and by
  `maxPerHour`.

## Capabilities

### New Capabilities

- `action-node-cordon`
- `action-node-drain`
- `action-pvc-expand`

## Impact

- `internal/action/node`: a new package. Node actions are cluster-scoped and
  reason about pods across every namespace, which is a different shape from
  the workload actions.
- Three action keys in the chart, off by default. `node.drain` grants the
  widest permission remedik has: `list` on pods cluster-wide and `create` on
  `pods/eviction`. That is stated plainly in the RBAC table.
