## Context

`prune` runs inside `finish`, keeps the newest `keepPerStrategy` terminal
records for the strategy that just completed, and deletes the rest. It is
correct for what it does: it stops an alert storm growing etcd without bound.

What it cannot do is reclaim anything for a strategy that is not currently
finishing remediations, which is every strategy that has been disabled,
renamed, deleted, or has simply gone quiet.

## Decisions

### 1. A sweeper, not a longer list of triggers

The alternative is to prune more often from more places — on strategy delete,
on startup, on a watch event. Each is a hook that can be missed, and "was
this path covered" becomes a question with a growing answer.

A sweeper on a timer has one property those do not: it converges. Whatever
state the cluster reached, however it got there, the next sweep applies the
policy. Retention is a statement about the steady state, so it should be
enforced by something that runs in the steady state.

### 2. It is leader-only, and that is not optional

Two instances sweeping would race on deletes — harmless in itself, both would
succeed or one would 404 — but they would also both hold the full list in
memory and both walk it. More importantly the sweeper is the one thing here
that deletes without a remediation having happened, and "exactly one instance
acts" is the rule the lease exists to enforce.

### 3. The guards set a floor that maxAge cannot cross

This is the part that would be a real incident if it were got wrong.

Guard state lives in memory and is rebuilt from existing `Remediation`
resources at startup. So a record is not only history: inside a strategy's
cooldown window it is *the reason remedik will not act again*. Delete it, and
after the next restart remedik remediates something it had correctly refused.

So the floor is computed from the strategies as they are now — the longest
cooldown, and the window `maxPerHour` implies — and nothing newer than that is
ever deleted, whatever `maxAge` says. When the floor overrides the configured
age, the sweeper logs it once per sweep: a retention policy that is quietly
not being applied is worse than one that is refused.

A margin is added on top, because a strategy's cooldown can be lengthened
between sweeps and the records that would have covered the new window must
still be there.

### 4. Age is measured from completion, not creation

`completedAt` is when the thing happened. A record created a month ago and
still `Pending` is not old history — it is work in flight, and it is not a
candidate at all. Only terminal records are swept, which the existing prune
already gets right.

### 5. Unset means unchanged

`maxAge` empty is today's behaviour exactly, so an upgrade cannot delete
anybody's history because a default looked reasonable. The sweeper still runs,
because the orphan-reclaim half is a bug fix rather than a new policy — but
with no age limit it only applies `keepPerStrategy`, which is what the
operator already asked for.

## Risks / Trade-offs

- **A sweep is a LIST of every record in the namespace.** It reads through the
  cache, so the cost is memory already spent rather than API calls, and it
  runs infrequently. At the scale where that stops being true, retention is
  the thing keeping it smaller.
- **Deleting in bulk makes watch events in bulk.** The sweeper deletes at a
  bounded rate for that reason; a sweep that takes several minutes is fine.
- **The floor can make `maxAge` ineffective** if somebody sets a 30-day
  cooldown and a 7-day retention. That combination is contradictory and the
  log says which one won.
