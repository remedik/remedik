## Context

`dryRun` is global. The owner wants the combination — act here, report
there — and the interesting part is not the mechanism but where the setting
lives, because each answer has a different failure mode when the setting and
the RBAC disagree.

## Decisions

### 1. The setting lives in the chart, not on a Namespace and not on a strategy

Three places were possible.

**A label on the `Namespace`** reads well: the team that owns the namespace
says what may happen in it. It is wrong. remedik's RBAC is cluster-wide,
granted once by whoever installed it, on the strength of a set of actions
they reviewed. A namespace label lets anyone with `edit` on that namespace
promote themselves from "reported" to "remediated" — using permissions
somebody else granted for a reason that did not include them. The failure
mode is silent and one-directional: nothing errors, remedik simply starts
acting where it was not meant to.

**On the strategy** puts the decision with whoever wrote the remediation.
But a strategy matches by alert labels and spans namespaces, so it cannot
express "this rule, live in staging and reporting in prod" without being
copied — which is the two-installs problem again, one level down.

**In the chart** puts it with the person who granted the permissions, in the
same file, reviewed in the same commit. Posture and RBAC disagreeing is then
a diff somebody reads, not a discovery made during an incident. It costs a
`helm upgrade` to change, which is the correct amount of friction for
"remedik may now modify production".

### 2. The posture is resolved once, at creation

The alternative — reading the current posture at execution time — means a
record can be created under one posture and run under another, and the
record no longer explains itself. `steps` and `retries` are already copied
for exactly this reason.

This removes the reconciler's `rem.Spec.DryRun || r.DryRun`. That OR existed
as a safety net: flipping the operator to dry-run also stopped anything
already recorded. It cannot survive per-namespace posture, because "live in
staging while the default is dry-run" is precisely a record whose posture
disagrees with the operator's default — the OR would silently simulate it,
and the feature would not work.

What is lost is narrow and already better served: a pending retry keeps the
posture it started with. Anybody who wants everything to stop scales the
deployment to zero, which is instant, or disables the strategy. Changing
`dryRun` never was the fast path — it needs a rollout either way.

### 3. The namespace is the target's, not remedik's

`staging` means "the workload in staging", not "remedik happens to run in
staging". The target is already resolved before the record is created, for
the cooldown guard, so this costs nothing.

### 4. A target with no namespace uses the default

`node.drain`, `webhook.call`, `job.run` — cluster-scoped or outward-facing,
with no namespace to look up. Falling back to the default is the only
answer that is not a guess, and it is safe because the default ships as
dry-run: an operator who has enabled node actions has already made a
deliberate decision about the global posture.

The alternative — refusing to run a targetless action while any override
exists — punishes a correct configuration for the sake of tidiness.

### 5. An unknown namespace is not an error

A posture entry naming a namespace that does not exist is a typo, and the
right response is to say so once at startup rather than to fail. remedik
does not watch namespaces, and a namespace can be created after the
install; treating absence as an error would make the chart's ordering
matter.

The operator logs the entries it was given at startup, and the dashboard
shows them, so a typo is visible in the two places somebody looks.

## Risks / Trade-offs

- **Someone will read `dryRun: true` and believe nothing acts.** That is the
  real cost of this feature, and no naming fixes it entirely. It is mitigated
  by making the overrides impossible to miss: the chart's NOTES print them
  after every install, the dashboard's badge says "mixed" rather than
  "dry-run", and every record carries the posture it ran under.
- **A `helm upgrade` to change posture.** Deliberate. See decision 1.

## Open Questions

Whether a namespace *selector* — label-based rather than a list — is worth
having. It would suit a cluster with fifty namespaces and a naming
convention, and it reintroduces exactly the escalation risk from decision 1
unless the selector matches on labels only the cluster operator can set.
Left out until somebody has the fifty namespaces.
