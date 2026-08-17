## Why

There is a retention policy — `history.keepPerStrategy: 200` — and it is not
enough. It has three gaps, and the first is an unbounded leak.

**1. Pruning only runs when a remediation completes for that strategy.** It
lives inside the terminal write. So:

- A strategy that fired 200 times and then went quiet keeps 200 records
  forever.
- A strategy that is **disabled** stops pruning, and keeps everything it ever
  made.
- A strategy that is **deleted or renamed** leaves its records with nobody to
  prune them. Nothing in remedik will ever look at them again.

Over the life of a cluster, strategies are added, renamed and removed. Every
one of those leaves a permanent deposit. That is a leak, not a policy.

**2. There is no age limit.** An operator cannot say "keep thirty days",
which is how retention is actually expressed — in a data policy, an audit
requirement, or a conversation with whoever owns etcd. 200 records may be a
week for one strategy and three years for another.

**3. The total is unbounded in the number of strategies.** The limit is per
strategy, so forty strategies is eight thousand records. Measured on a dev
cluster: a record is ~3.5 kB, so that is ~28 MB in etcd against a 2.1 GB
default quota — survivable, but it is also 8000 objects the operator holds in
its informer cache and re-lists on every dashboard render.

None of this is urgent in a small cluster. All of it is the kind of thing that
is discovered eighteen months in, by somebody who did not install it.

## What Changes

- **`history.maxAge`**, an age limit applied to terminal records regardless
  of count. Unset means today's behaviour.
- **A sweeper** that applies retention on a schedule rather than only when a
  remediation completes, so a quiet, disabled, renamed or deleted strategy's
  records are reclaimed too.
- **A floor that protects the guards.** Guard state is rebuilt from existing
  records at startup, so deleting a record inside a strategy's cooldown window
  would make remedik forget a cooldown it is enforcing and remediate again.
  The sweeper SHALL never delete a record newer than the longest guard window
  currently configured, whatever `maxAge` says, and SHALL say so when it
  overrides.
- **Metrics**, so retention is observable rather than a thing that silently
  either works or does not.

## Non-goals

- **Archiving anywhere.** Records are Kubernetes objects; shipping them
  somewhere durable is a job for whatever already collects Kubernetes events
  and audit logs. remedik deleting its own history is in scope; remedik
  running a data pipeline is not.
- **Retaining failures longer than successes.** Tempting, and it makes the
  policy two policies with an interaction to reason about. If it is wanted
  later it is a separate argument.
- **Compacting etcd.** Not remedik's business, and doing it would need
  permissions the chart must not have.

## Capabilities

### Modified Capabilities

- `remediation-execution`

## Impact

- `internal/engine`: a sweeper runnable and the age limit.
- `charts/remedik`: one value; no new RBAC — delete on `remediations` is
  already granted for pruning.
- The sweeper runs on the leader only.
