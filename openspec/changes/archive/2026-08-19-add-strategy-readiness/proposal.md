## Why

A strategy is the one object a user of remedik writes themselves. It is also
the only one that never says whether it worked.

`RemediationStrategy.status` exists — conditions, `executionCount`,
`lastExecutionTime`, `observedGeneration` — and nothing in the operator has
ever written it. Three places already promise otherwise:

- the API comment on the status: *"'Ready' reports whether the strategy is
  usable: a strategy referencing an unknown action is accepted by the schema
  but not Ready"*;
- `Registry.ValidateNames`, whose comment says *"The engine calls it when a
  strategy is applied, so an unusable strategy is reported on the resource
  rather than discovered mid-incident"* — it has no caller outside its test;
- the dashboard, which renders a `NotReady` message from
  `status.conditions` on every row of `/strategies`.

The two print columns on the CRD, `Runs` and `Last Run`, are empty in every
cluster this has ever run in.

The consequence is the failure this project exists to avoid. A strategy that
names `pod.delete` while the chart has `actions.podDelete.enabled=false`, or
that says `deployment.restrat`, is accepted by the API server, looks correct
in `kubectl get`, and is discovered at 03:00 as a `Remediation` that failed
with `UnknownAction`. The check is written, the RBAC for the status
subresource is already granted by the chart, and the UI is already built —
what is missing is the controller between them.

## What Changes

- **A strategy controller** that reconciles `RemediationStrategy` and writes
  its status. It executes nothing and reads no workloads; its whole job is to
  answer "is this strategy usable, and is it being used?".
- **A `Ready` condition.** False with reason `UnknownAction` when a step —
  or an `onFailure` step — names an action this build does not have, with a
  message listing the actions it does have. True otherwise.
- **`executionCount` and `lastExecutionTime`**, derived from the records the
  strategy has produced, so `kubectl get remediationstrategies` answers
  "has this ever fired?" without a second query.
- **A `Ready` print column**, because the answer belongs where a user
  already looks.
- **The claims that were not true are made true or removed** — the comment
  about admission-time validation of action names describes something that
  does not exist, and the counter's documented meaning is changed to what a
  derived counter can honestly promise.

## Non-goals

- **An admission webhook.** It would reject an unusable strategy at apply
  time rather than mark it, which is better, and it is a certificate, a
  failure mode when the operator is down, and an argument about
  `failurePolicy`. A condition needs none of that and reaches the same
  reader. Worth revisiting if the CRD grows validation that cannot be
  expressed as a condition.
- **Validating step parameters.** `with` keys are read per action and a typo
  in one is silently ignored today. That is a real gap and a separate change:
  it needs each action to declare its parameters, which is the same
  groundwork `ActionPlugin` needs.
- **Blocking execution on `Ready`.** The condition reports; it does not gate.
  A strategy that is not Ready already fails at its first step, and making
  the report a gate would mean a controller lagging behind an apply could
  suppress a remediation that would have worked.
- **Counting executions the cluster no longer holds.** The counter is derived
  from records, so retention lowers it. A monotonic counter would need a
  write on the alert path, which is the one path that must stay cheap during
  a storm.

## Capabilities

### Modified Capabilities

- `remediation-strategies`

## Impact

- `api/v1alpha1`: no schema change beyond one print column; two comments that
  described unbuilt behaviour are corrected.
- `internal/engine`: a second reconciler, `strategy.go`, watching strategies
  and the records that name them.
- `cmd/remedik`: it is registered with the manager, so it is leader-elected
  like the other one.
- No new RBAC: `remediationstrategies/status` is already granted by the
  chart, for this.
