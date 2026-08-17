> **Implemented 2026-08-17**, after a first attempt was reverted the same
> day. Three separate mistakes were in that attempt, and each one is worth
> keeping because none was visible without running the whole loop:
>
> 1. **A status-conflict retry shipped alongside it**, which was the real
>    damage. `Reconcile` reads through the manager's cache, so a second
>    reconcile can read a copy still saying `Running` after the first has
>    recorded `Succeeded`, and by invariant 3 decide the remediation was
>    interrupted. Its write is refused because it carries an old
>    `resourceVersion` — that refusal is the only thing protecting the
>    record. Retrying it let a false `Interrupted` overwrite a `Succeeded`.
>    `internal/engine/staleread_test.go` holds that now and fails if it
>    returns. **This change must never bring a conflict retry with it.**
>
> 2. **Every HTTP server still needed the lease**, which is
>    controller-runtime's default for a runnable that says nothing. So a
>    standby had no gateway, dashboard or metrics listener at all and refused
>    the connection instead of answering 503 — defeating the reasoning below
>    entirely. `NeedLeaderElection() == false` is load-bearing, and
>    `cmd/remedik/main_test.go` says so.
>
> 3. **Readiness was then gated on leadership**, which looked right and was
>    not: a standby never becomes ready, so `helm --wait` and `kubectl
>    rollout status` never finish with two replicas — the failover this
>    change exists to allow could not be installed. A standby is ready
>    because waiting is its job.
>
> `make e2e` is what found all three. It reports 116 of 116 with this
> applied.

## Why

`replicas: 1` is in the chart and nothing enforces it. A `kubectl scale
deploy/remedik --replicas=2` succeeds, and the second instance is not idle:
it serves the gateway behind the same Service and reconciles the same
resources.

The guards are in-memory and rebuilt from existing `Remediation` resources
at startup — an invariant this project states plainly. Two instances mean
two sets of guard state, so a cooldown one instance is enforcing is invisible
to the other. The alert storm remedik exists to absorb would be amplified
instead: two gateways accepting, two sinks creating, two reconcilers acting.

The requirement is not written down anywhere either. Somebody scaling for
availability would be doing the reasonable thing.

## What Changes

- **Leader election**, so that exactly one instance reconciles and exactly
  one accepts alerts, whatever the replica count.
- **The gateway answers 503 on a non-leader**, naming the reason. Alertmanager
  retries a non-2xx, and the Service sends the retry to another pod, so a
  scaled deployment degrades to "slightly slower" rather than "twice as
  much remediation".
- The lease permission is added to the chart's Role, justified by this
  feature, as the RBAC rule requires.
- Replicas become configurable, defaulting to one. More than one is now a
  failover story rather than a hazard.

## Non-goals

- **Sharing guard state between instances.** The guards are in memory
  because the decisions that matter most should be the easiest to test, and
  a distributed cooldown is a different project. Leader election makes the
  in-memory design correct rather than working around it.
- **Active-active.** One leader acts; the others wait. For an operator whose
  work is bounded by how fast a cluster reconciles, throughput was never the
  constraint.

## Capabilities

### Modified Capabilities

- `remediation-execution`

## Impact

- `cmd/remedik`: leader election on the manager, and the gateway gated on it.
- `internal/gateway`: a readiness predicate the handler consults.
- `charts/remedik`: `replicaCount`, the lease rule, and a NOTES line.
