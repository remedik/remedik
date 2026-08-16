## Context

Two guards exist and both are about time. The third is about state, which is
a different shape: it needs to look at the cluster, and everything in
`internal/guards` deliberately does not.

## Decisions

1. **The guard asks whether the workload is fragile now, not how fragile it
   would be afterwards.** Predicting the effect of each action is a
   simulation — a restart maintains availability, an eviction removes one
   pod, a drain removes many — and every action would need to model itself
   correctly for the guard to be right. A simulation that is wrong is worse
   than a rule that is blunt, because people trust it more. "Is this
   workload already too degraded to touch?" is answerable from status, the
   same way a human answers it.

2. **Two limits, one absolute and one relative.** `minAvailable` is "never
   touch the last one" and is the rule people can state without thinking.
   `maxUnavailablePercent` is "do not add to something already struggling"
   and is the one that scales from a 3-replica service to a 300-replica one.
   Neither subsumes the other.

3. **The guard fails closed.** If the workload cannot be read — no
   permission, an API error, a kind the guard does not understand — it
   refuses. A guard that allows when it cannot evaluate is not a guard; it
   is a comment. The refusal names the cause, so a missing permission
   surfaces as a decision on an event rather than as remediation quietly
   stopping.

4. **"Not applicable" is not the same as "cannot evaluate".** A node has no
   replica count and an action that touches nothing has no workload, so
   there is nothing for this guard to measure and it allows. That is a
   different answer from "I tried to read it and could not", and conflating
   them would make the fail-closed rule either useless or paralysing.

5. **A pod resolves to its controller.** `pod.delete` targets a pod, and the
   question is about the workload behind it, so the guard walks
   `ownerReferences` — pod to ReplicaSet to Deployment, or pod straight to
   StatefulSet or DaemonSet. A pod with no controller is refused by
   `pod.delete` itself before the guard is reached, so the walk never has to
   invent an answer.

6. **Reads go through a direct client, not the cache.** Caching every
   Deployment in the cluster to look at a handful during incidents would
   cost memory permanently for an occasional read, and would force the chart
   to grant `list` and `watch` where `get` is enough. This is the same
   argument the actions already make.

7. **`internal/guards` stays free of Kubernetes types.** The health reading
   arrives through an interface returning a plain struct, implemented in
   `internal/engine`. The dependency runs the same way as everywhere else in
   this project: the package that decides declares what it needs, and the
   package with the client provides it.

## Risks / Trade-offs

- **A cluster that enables `blastRadius` without the RBAC stops
  remediating.** That is the fail-closed rule working, and it is loud: every
  refusal is an event on the strategy naming the permission. The chart
  grants the reads with the same value that turns the guard on, so the two
  can only disagree if somebody sets them apart on purpose.
- **One more read per alert**, on a path that runs during incidents. A
  single `get` against the API server, only for strategies that configure
  the guard.
- **The percentage is measured against desired replicas**, so a workload
  scaled to zero reads as "nothing unavailable" rather than as an error.
  That is the honest reading: there is nothing to protect.

## Open Questions

None blocking. Bounding node actions by how much of the *cluster* is already
unavailable is a real and different guard, and belongs with the change that
introduces them.
