## Context

`SetupWithManager` builds the controller with no options, so
`MaxConcurrentReconciles` is controller-runtime's default of 1.

Everything about the state machine was designed around one attempt per
reconcile, which is what makes `Running` mean "the process died". That property
is per object, and controller-runtime already guarantees a single object is
never reconciled by two workers at once. So the serialisation across *different*
objects buys nothing the design relies on.

## Decisions

### 1. The default is 4, and it is not a CPU count

The instinct is `runtime.NumCPU()`. It is wrong here. This number does not
govern how much work remedik does; it governs **how many things remedik changes
in somebody's cluster at the same moment**. Sizing that by the machine the
operator happens to be scheduled on would make the blast radius a property of
the node pool.

Four is enough that one slow pipeline handover does not stall an incident, and
small enough that a misconfigured strategy during a storm is still four
concurrent restarts rather than thirty-two.

### 2. It is safe, and the reasons are worth writing down

- **One object at a time** is controller-runtime's guarantee, so the `Running`
  invariant and its conflict-refusal are untouched.
- **The guards hold a mutex**, and their state is written on completion.
  Concurrent completions interleave; none of the guard questions —
  last-completion, starts-since — depend on ordering between different
  targets.
- **Guard decisions are made in the sink, at creation**, not at execution. So
  concurrency changes when a permitted remediation runs, never whether it was
  permitted.
- **Pruning tolerates a concurrent delete**: it ignores NotFound, and the
  record it must not delete is the one that just finished, which is by
  construction the newest.

### 3. Two remediations may now touch one workload at the same time

They could already, in sequence — the cooldown is scoped by strategy and
target, so two different strategies matching the same workload were always
possible. What changes is that the two can now overlap.

For every built-in action this is benign: they are declarative patches whose
last write wins, and each verifies its own result. It is stated here rather
than discovered, because the answer for `script.run` is whatever the operator's
script does, and that is their call to make.

### 4. It is a chart value because the right number is a property of the cluster

A cluster with two strategies and a quiet week wants 1. A platform team running
this across a fleet wants more. The value carries its reasoning in
`values.yaml`, where somebody changing it will read it.

## Risks / Trade-offs

- **More simultaneous change during an incident.** That is the point and also
  the risk, which is why the default is deliberately low and the value is
  described as blast radius rather than throughput.
- **The API server sees more concurrent writes.** Bounded by the same number,
  and small beside what a rollout already generates.
