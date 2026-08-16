## Context

The highest-risk actions in the catalogue, landing last and on purpose:
`blastRadius` exists, the contract has verification, and every pattern these
follow has been in use for a while.

## Decisions

1. **Cordon and drain are separate verbs.** `kubectl drain` cordons as part
   of draining, and a strategy that only wants "stop scheduling here" would
   have to accept eviction to get it. They are different decisions with
   different costs: cordoning is free and reversible, draining moves a
   node's worth of work during an incident. `node.drain` still cordons
   first, because draining without it is a race against the scheduler.

2. **A drain evicts, and retries the refusals.** Eviction is the only call
   that consults PodDisruptionBudgets, and during a drain a 429 is *normal*
   rather than fatal: it means "not yet". `kubectl drain` retries, and so
   does this, until the step's timeout. That is the one place in remedik
   where a 429 is not an immediate failure, and the difference is that a
   drain is a loop by nature while `pod.delete` is a single act.

3. **A drain that does not finish is a failure.** Half-drained is the worst
   state to leave a node in: it is cordoned, has lost some of its work, and
   nobody knows whether to continue or reverse. Reporting a partial drain as
   success would leave capacity missing that no dashboard accounts for. The
   step fails, naming how many pods are left and which ones, and the node
   stays cordoned — because uncordoning a node somebody is mid-way through
   draining would be worse.

4. **DaemonSet pods are skipped, not evicted.** Their controller puts them
   straight back, so evicting them is a loop that never ends. `kubectl
   drain` requires `--ignore-daemonsets` for the same reason; here it is the
   default, because a remediation that needs a flag to terminate is a
   remediation that will be run without it.

5. **Mirror pods and pods with no controller are skipped by default.** A
   static pod cannot be evicted at all, and a bare pod is not coming back.
   Evicting the second is deletion, which `pod.delete` already refuses for
   the same reason; a drain that quietly did it would be a bigger version of
   the same mistake.

6. **`pvc.expand` checks the StorageClass first.** Where
   `allowVolumeExpansion` is not true, the API server accepts the patch and
   nothing happens — a remediation that reports success and changes nothing,
   which is the worst outcome available. The check turns that into a refusal
   naming the StorageClass.

7. **`pvc.expand` never shrinks.** Kubernetes cannot, so a step asking for
   less is a mistake to report rather than a request to attempt.

8. **Node actions are their own package.** They are cluster-scoped and they
   reason about pods across every namespace; the workload package is about
   one object in one namespace. Keeping them apart keeps each honest about
   what it reaches.

## Risks / Trade-offs

- **`node.drain` holds the widest permission remedik has**: `list` on pods
  cluster-wide, and `create` on `pods/eviction` everywhere. There is no
  narrower way to drain a node. It is off by default, its rules are in the
  RBAC table with this sentence beside them, and the action itself is the
  one most worth leaving in dry-run for a long time.
- **A drain occupies the reconcile worker for its duration.** Executions are
  serialised, so a five-minute drain is five minutes in which no other
  remediation runs. That is the cost of the existing design, and it is
  bounded by the step's timeout, which the strategy states.
- **Cordoning is easy to leave behind.** A cordoned node stays cordoned
  until somebody uncordons it, and remedik does not do that automatically —
  it has no way to know whether the underlying problem is fixed. The record
  and the events say what happened; `node.uncordon` exists for the strategy
  that wants to say when.

## Open Questions

Whether a resolved alert should uncordon automatically is a real question,
and it belongs with auto-close of remediations on `resolved` alerts —
deliberately out of scope since the MVP, and unchanged here.
