## Why

Every strategy remediates without asking. `execution.mode` exists, is
documented, and accepts exactly one value.

That is the largest gap between remedik and what it was designed to be. The
original plan makes human-in-the-loop the headline and `approval` the **default
for destructive actions** — "mode: auto is an explicit choice by the user, not
the default". Today it is the only choice, so a team that wants a person to look
at a node drain before it happens has to leave the strategy disabled and run
things by hand.

The reason it was deferred is that approval was scoped with a Slack bot, and the
bot is a large piece of work against a service that cannot be tested from a
checkout. But **the gate does not need Slack.** A human can approve the way
every other decision in this project is made:

```bash
kubectl -n remedik patch remediation node-drain-x7k2q --type merge \
  -p '{"spec":{"approval":{"decision":"approve","by":"dana"}}}'
```

That is GitOps-able, it is in the audit trail by construction, and it needs
nothing outside the cluster. Slack becomes a nicer front end for the same gate
rather than a prerequisite for having one.

## What Changes

- **`execution.mode: approval`** — the remediation is created, reaches
  `AwaitingApproval`, and waits. Nothing is resolved, planned or executed until
  somebody decides.
- **`execution.mode: manual`** — never starts from an alert. For the strategies
  a team wants to keep behind a red button.
- **`spec.approval`** on the record: a decision, who claims to have made it, and
  an optional note.
- **`execution.approvalTimeout`** — no decision by then and the remediation
  fails as `ApprovalTimeout` and **escalates**, which is what the plan asks for:
  silence must reach somebody.
- **The dashboard shows what is waiting**, because a queue nobody can see is a
  queue nobody empties.

## Non-goals

- **The Slack bot.** A separate change, and a smaller one once the gate exists:
  it posts the card and issues the same patch.
- **Trusted attribution.** `by` is what the patcher claims. The cluster's audit
  log is the authority on who actually issued the patch, and the docs say so
  rather than implying remedik verified it. An admission webhook or the Slack
  identity model is how that becomes trustworthy, and neither is this change.
- **Changing any default.** Existing strategies say `auto` or nothing, and
  nothing about them changes. Making `approval` the default for node actions is
  a separate argument about the cookbook, not about this mechanism.

## Capabilities

### Modified Capabilities

- `remediation-execution`

## Impact

- `api/v1alpha1`: one enum widened, one struct, one state, two reasons.
- `internal/engine`: the sink honours the modes; the reconciler owns the wait.
- `internal/dashboard`: waiting records are surfaced.
- No new RBAC: approving is a patch on `remediations`, which is a human's own
  permission and not remedik's.
