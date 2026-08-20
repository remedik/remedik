## Context

Everything this change needs already exists except the thing that connects it.
`Registry.ValidateNames` answers the question, `RemediationStrategyStatus`
holds the answer, the chart grants `remediationstrategies/status`, and the
dashboard renders it. There has never been a controller for the resource,
because until now nothing wrote to it — the sink only reads strategies, with
`UnsafeDisableDeepCopy`, precisely so that it never writes one.

## Decisions

### 1. A second reconciler, not a hook in the sink

The obvious shortcut is to bump the counter where the record is created. It is
the wrong place twice over.

The sink runs on the gateway's own goroutine, and a delivery during a storm is
hundreds of alerts. Adding a read-modify-write of a cluster-scoped object per
alert puts an API round trip on the path that must stay cheap — the same
reasoning that made the sink read strategies once per delivery rather than once
per alert. And the sink holds pointers into the manager's cache: writing through
one is a data race with every other reader of that cache.

A controller has neither problem. It reconciles off a work queue, it coalesces
bursts by key, and it owns a copy.

### 2. `Ready` reports; it does not gate

A strategy that is not Ready still matches alerts and still creates records,
which then fail at their first step with `UnknownAction` exactly as they do
today.

Gating on the condition would mean a controller that has not caught up with an
apply can suppress a remediation that would have worked — the condition would
become a second, slower source of truth about what remedik will do. The registry
is the fast one and stays the only one. What the condition adds is that the
answer arrives seconds after `kubectl apply` instead of during an incident.

The same reasoning is why `enabled: false` does not make a strategy not Ready.
Ready is about whether the strategy *could* run, not whether it *will*; the
dashboard and `kubectl get` already show `Enabled` beside it.

### 3. The counter is derived, and its documentation changes to say so

`executionCount` is the number of `Remediation` records the strategy has
produced that the cluster still holds, and `lastExecutionTime` is the newest
one's creation timestamp. Retention prunes records, so the count can go down.

The alternative — a monotonic counter incremented at creation — is more
faithful to the field's original comment and needs the write on the alert path
that decision 1 refuses. So the comment changes instead. It is what the rest of
the dashboard already means by its numbers: the history the cluster still has.
Rates stay a question for `remedik_remediations_total`, which is monotonic
because a metric can afford to be.

### 4. This status write may be retried on conflict, and invariant 3 still holds

The rule that a `Remediation` status write is never retried on conflict is
about a *verdict*: a second reconcile can hold a stale opinion about an
execution that already finished, and the conflict is what refuses it.

This status is not a verdict. Every field is derived from observed state and
recomputed from scratch on each pass, so a conflict means "something changed,
look again" and looking again produces the right answer by construction. It is
returned to the work queue like any other transient error.

### 5. The write is skipped when nothing changed

A status write is a watch event, and a controller that writes on every pass
reconciles itself forever. Each pass builds the status it wants and compares;
identical means return without writing. Condition timestamps make this
sharper than it sounds — `meta.SetStatusCondition` keeps `lastTransitionTime`
when the status is unchanged, so an unchanged condition really is byte-identical.

### 6. The records are found by label, not by an index

`remedik.dev/strategy` is already on every record and already used this way by
the reconciler's pruning. A field index on `spec.strategyName` would be
marginally faster and is a second thing to keep correct; the label is the
established path and the list is served from cache.

### 7. Escalation steps are validated too

`onFailure.steps` names actions from the same registry, and an escalation that
cannot run is the failure mode with the least visibility in the whole project —
it is discovered when a remediation has already failed. The condition names the
step it found, and says which list it was in.

## Risks / Trade-offs

- **The counter can move backwards** when retention prunes. Accepted, and
  documented on the field: it is a coarse counter for humans, beside a metric
  that is not.
- **The condition lags an apply by the time it takes to reconcile.** Sub-second
  in practice; and because it does not gate, the lag has no consequence beyond
  when the answer appears.
- **One more controller in the manager**, sharing the lease. It does nothing
  but list from cache and occasionally write a status, so it competes with
  nothing.
