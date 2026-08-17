## Why

One remediation at a time, cluster-wide, for as long as it takes.

The reconciler runs with controller-runtime's default of one worker, so every
`Remediation` in the cluster is executed strictly after the one before it. The
code names this — `MaxVerifyTimeout` is capped "because executions are
serialised, so a long check is time no other remediation can use" — but names
it as a constraint rather than solving it.

What the constraint costs, from the values the CRD already permits:

| | |
| --- | --- |
| a step's verify ceiling | 10 minutes |
| steps per attempt | up to 8 |
| attempts | up to 11 |
| escalation | 2 minutes |
| **one remediation, worst case** | **15 hours** |

The realistic case is worse than it sounds because it is ordinary: a `job.run`
that hands an incident to a pipeline and waits for the verdict — which the
cookbook recommends, because a pipeline API answers "queued", not "succeeded" —
takes minutes by design. Two retries and it is half an hour during which
nothing else in the cluster is remediated.

That is precisely backwards for the product. remedik exists to absorb an alert
storm; a storm is many alerts about many workloads at once, and the current
design handles them one at a time behind whichever one is slowest.

## What Changes

- **`MaxConcurrentReconciles`**, set from a new chart value, so remediations
  for different resources execute at the same time.
- **A conservative default of 4**, not the number of CPUs: this bounds how
  many things remedik changes in a cluster simultaneously, which is a
  blast-radius decision and not a throughput one.
- **The value is documented as what it is** — how many remediations may be
  acting at once — because an operator sizing it is reasoning about risk, not
  about parallelism.

## Non-goals

- **Concurrency within one remediation.** Its steps stay strictly ordered:
  step two acts on step one's result, and the record is a sequence somebody
  reads during an incident.
- **Raising `MaxVerifyTimeout`.** It is capped for a second reason that still
  holds — a check that runs for an hour is indistinguishable from a stuck
  operator, whatever else is running.
- **A work queue of remedik's own.** controller-runtime already guarantees one
  reconcile per object at a time, which is the property the state machine
  needs. Adding a second queue would mean owning that guarantee.

## Capabilities

### Modified Capabilities

- `remediation-execution`

## Impact

- `internal/engine/controller.go`: one option on the builder.
- `charts/remedik`: one value.
- No new RBAC. No change to what may be remediated, only to how many at once.
