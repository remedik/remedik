## Why

The flow this project exists to serve has an end it cannot reach:

> alert → remedik → remediate, or trigger a pipeline → it worked, fine →
> **it did not work, tell somebody**

Everything up to the last arrow works. `onFailure` holds one field,
`retries`, and when those run out the remediation is recorded as `Failed`
and nothing else happens. The record is there for whoever goes looking, and
nobody goes looking at 3am for a remediation they did not know was
attempted.

What exists today is rate-based: a `PrometheusRule` fires when *most*
remediations are failing. That is the right alert for "remedik is broken"
and the wrong one for "this remediation did not fix this incident, and the
incident is still happening".

## What Changes

- **`onFailure.steps`** — a second plan, run when the first has failed and
  no retries remain. Escalation is then an action like any other:
  `webhook.call` to PagerDuty, `job.run` to a pipeline, whatever the cluster
  already uses.
- **`status.escalation`** records it separately from the remediation's own
  steps, because "the remediation failed" and "telling somebody failed" are
  different facts and an operator needs both.
- The terminal state stays `Failed`. Escalating is not succeeding, and a
  record that went green because somebody was paged would be the most
  misleading thing in this project.
- Escalation steps run **even in dry-run**, and this is the one deliberate
  exception in remedik. A dry-run trial is exactly when an operator wants to
  see the escalation path work, and `webhook.call` to a test endpoint
  changes nothing in the cluster. What the escalation is *told* says which
  it was.

## Non-goals

- A notification subsystem. remedik already has verbs that reach outside;
  escalation is one of them pointed at a different endpoint.
- Escalating a guard rejection. A guard refusing is the system working, and
  paging somebody for it would teach them to ignore the page. Those are
  already events on the strategy and a metric.
- Retrying the escalation. If paging fails, the remediation record says so;
  looping on a failed page during an incident helps nobody.

## Capabilities

### Modified Capabilities

- `remediation-execution`

## Impact

- `api/v1alpha1`: `OnFailure.Steps` and `RemediationStatus.Escalation`;
  `make generate manifests`.
- `internal/engine`: the reconciler runs the escalation plan on the terminal
  failure path.
- `internal/dashboard`: the detail page shows the escalation when there is
  one — a failed remediation nobody was told about looks very different from
  a failed one that paged.
- No new RBAC: escalation steps are ordinary actions, each already gated by
  its own permission.
