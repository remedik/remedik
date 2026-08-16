## Why

remedik has two guards and both answer questions about *time*: how recently
did this run, and how often. Neither can answer the question that matters
when the action removes capacity rather than replacing it —

> how much of this workload is already broken?

`pod.delete` evicts a pod. The Eviction API refuses when a
PodDisruptionBudget says so, which is the right mechanism and the wrong
coverage: most workloads have no PDB, and the ones that do have one written
by the workload's owner, not by whoever wrote the remediation. Those are
different people with different concerns, and only the second one knows that
an automated system is about to act unattended.

The gap gets worse with the node actions that come next. `node.drain`
removes a node's worth of capacity at once, during an incident, and neither
`cooldown` nor `maxPerHour` can express "not while a quarter of that
workload is already down".

This lands **before** the node actions rather than after, because shipping
destructive verbs and then bounding them means every cluster that upgraded
in between ran them unbounded.

## What Changes

- **`guards.blastRadius`**, a third guard with two limits, both opt-in:
  - `minAvailable` — refuse while the workload has this many available
    replicas or fewer. "Never touch the last one."
  - `maxUnavailablePercent` — refuse while this share of the workload is
    already unavailable. "Do not add to a workload that is already
    struggling."
- The guard reads the workload the target belongs to, resolving a pod to
  its controller, and **does not apply** where there is no workload to
  measure — a node, or an action that touches nothing.
- The guard package stays free of Kubernetes types: it takes the health
  reading through an interface the engine implements, as the metrics
  package already does.

## Non-goals

- Replacing PodDisruptionBudgets. Eviction still consults them; this is a
  second opinion held by a different party, not a substitute.
- Modelling each action's effect. The guard asks whether the workload is
  fragile *now*, not how fragile it would be afterwards: predicting that
  per action is a simulation, and a simulation that is wrong is worse than a
  rule that is blunt.
- Bounding node actions by cluster capacity. A drain is bounded by
  PDB-honouring eviction and its own timeout; "how much of the cluster may
  be down at once" is a different guard and a different change.

## Capabilities

### Modified Capabilities

- `remediation-strategies`

## Impact

- `api/v1alpha1`: `Guards.BlastRadius`; `make generate manifests`.
- `internal/guards`: a third guard and its own health interface.
- `internal/engine`: a reader that resolves a target to a workload's health
  through the cache, and the Sink evaluating it.
- The chart gains `guards.blastRadius.enabled`, which grants the reads the
  guard needs: `get` on the three workload kinds, on pods, and on
  replicasets to resolve a pod to its Deployment. Off by default, like every
  other permission.
- **The guard fails closed.** A guard that cannot tell whether a workload is
  safe to touch must refuse rather than allow, so a strategy using
  `blastRadius` in a cluster that did not grant the reads stops
  remediating — visibly, with the reason on the event, rather than silently
  acting unbounded.
