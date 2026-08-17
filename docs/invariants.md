# What remedik promises never to do

This is the document to read before granting remedik write access to a cluster.

Everything below is a property the code is built to have rather than a rule the
code tries to follow, and each one says where that difference is enforced. Where
a promise is only a convention, this page says so — a security review that
cannot tell the two apart is not a review.

None of these changes without an [ADR](adr/).

---

## 1. Nothing an LLM produces is ever executed

remedik has no AI in its execution path and is not going to grow one.
LLM-backed features read and explain: they can summarise a failure or draft a
strategy for a person to review. What runs is a strategy somebody declared,
through guards somebody configured.

**Why it is structural:** the action registry is populated at startup from the
flags the chart sets. There is no path from generated text to a `Resolve`,
`Plan` or `Execute` call, because actions are Go types resolved by name from a
fixed registry, not code loaded at runtime.

The reasoning is in [ADR-0003](adr/0003-deterministic-core-ai-read-only.md).

## 2. Dry-run cannot be got wrong

Every action implements three separate methods: `Resolve` works out what it
would act on, `Plan` describes what it would do, `Execute` does it. A dry run
calls `Plan`. **The mutating code is never reached** — not skipped, not
short-circuited, not reached.

**Why it is structural:** a dry run and a live run take different code paths, so
forgetting a check cannot leak a write. The alternative — `if dryRun { return }`
inside each action — turns a guarantee into a convention that a new action can
silently omit, and it is refused for that reason.

The install default is dry-run, per namespace, and the posture is resolved once
when a record is created and written onto it. So every `Remediation` says which
posture it ran under, and a later configuration change cannot rewrite history.

## 3. A crash is never resumed

An attempt runs to completion inside a single reconcile. So a `Remediation`
found in `Running` can only mean one thing: the process died mid-execution. It
is failed as `Interrupted`, never resumed.

**Why:** resuming a half-finished mutating step safely would need per-action
idempotency guarantees that do not exist, and silently repeating an action is
the worse outcome. Waiting for a retry is `Pending`, never `Running`, which is
what keeps that reading true.

**The corollary that matters to a reviewer:** the status write is never retried
on conflict. `Reconcile` reads through a cache, so a second pass can see
`Running` after the first already recorded `Succeeded` — and the conflict on
that write is the only thing refusing the stale verdict. Retrying it would
record a successful remediation as failed.
`internal/engine/staleread_test.go` holds this.

## 4. Every permission exists because a named feature needs it

The chart grants a Kubernetes permission only because an action or guard you
enabled requires it. Disable the action and the RBAC rule disappears with it.

**Why it is checkable:** `hack/rbac-unchanged.sh`, run by `make verify`, proves
the chart grants nothing with every action disabled, that each action grants
only its own rules, and that the dashboard grants nothing at all. `helm
template` with your values is the complete answer to "what can this do".

Install it with everything off and it can do nothing. That is the intended
starting point.

## 5. An action's authority is named, never inherited

A remediation Job runs as the ServiceAccount its step names — never remedik's
own, which is **refused**. Secrets and ConfigMaps are read from remedik's own
namespace only.

**Why this one is load-bearing:** an alert's labels arrive from outside. If a
label could choose a credential or a ServiceAccount, then whoever can make an
alert fire could choose what remedik runs as. So the strategy names the
authority, and the alert can only ever supply a target.

## 6. Guards survive a restart before anything is accepted

Guard state lives in memory and is rebuilt from existing `Remediation`
resources at startup — synchronously, before the gateway accepts its first
alert.

**Why:** a restart that forgot its cooldowns would remediate everything again,
and a restart during an alert storm is exactly when that happens. With leader
election, the replay is tied to winning the lease rather than to the process
starting, so a standby that has been idle for hours does not take over
enforcing hours-old cooldowns.

A consequence worth knowing: retention never deletes a record inside a guard
window, whatever the configured age says, because that record *is* the reason
remedik will refuse to act.

## 7. The gateway answers 200 to anything it understood

Including "no strategy matched". Alertmanager retries a non-2xx, so a normal
outcome must not look like a failure.

An alert remedik has no opinion about is recorded as unmatched and nothing
happens. This is why routing broadly to remedik is safe.

## 8. The dashboard cannot write

Two independent layers, and both are deliberate:

- It is constructed from a `client.Reader`, so it **holds no method** that
  could write.
- A method allowlist answers anything but GET and HEAD with 405 **before
  routing**, so a page added later cannot opt out.

One makes a write impossible to call; the other makes it impossible to reach.
Writes belong to `kubectl` and, later, to an approval flow that has an identity
model — because an approve button whose audit trail cannot say *who* asked is
worse than no button.

## 9. It can be stopped in one command, and the stop is auditable

`kubectl patch configmap remedik-pause` forces dry-run on every replica within
seconds, with no restart. remedik's RBAC on that object is `get, list, watch` on
that one name — **it cannot un-pause itself**, because a switch the tool can flip
is not a switch.

Pausing does not silence it. Records keep appearing, marked `Simulated` and
labelled with the pause, so the audit trail says what was suppressed and why.

## 10. Escalation is bounded and never changes the outcome

`onFailure.steps` runs once the remediation has failed for good. It cannot make
a failed remediation succeed, it is never retried, and it is the one thing that
runs for real during a dry run — so the path can be proved before it is needed.

Every channel is tried: a failed escalation step does not skip the ones after
it, because they are alternative ways to reach a person rather than a sequence.
The record says whether anybody was told.

---

## What is *not* promised

Being explicit about the edges is part of the point.

- **remedik does not know whether your workload is healthy.** It knows what it
  remediated. Nothing in the product claims otherwise, including the
  namespaces page.
- **Guard decisions are made when a record is created**, not when it executes.
  A cluster that changes in between is a cluster remedik acted on with slightly
  stale information — bounded by how long execution takes, not by anything
  stronger.
- **`webhook.call`, `job.run` and `script.run` reach outside remedik**, and
  what happens there is your configuration's business. They are the widest
  trust surface in the project and are documented as such.
- **Two remediations may touch one workload at once.** They could already, in
  sequence; concurrency means they can overlap. For every built-in action this
  is benign — they are declarative patches that each verify their own result —
  but the answer for `script.run` is whatever your script does.
- **remedik is not on your paging path's happy side.** If you route an alert to
  remedik instead of on-call, remedik being down means that alert reaches
  nobody. [docs/routing.md](routing.md) is about the safety net that fixes
  this, and it is the section not to skip.

## Reporting a problem with any of these

[SECURITY.md](../SECURITY.md). If one of these promises is not actually kept,
that is a security issue and not a bug, whatever the impact looks like.
