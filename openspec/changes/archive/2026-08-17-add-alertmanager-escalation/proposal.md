## Why

`onFailure.steps` can page anybody with an HTTP endpoint, and the cookbook
shows PagerDuty and a pipeline. It cannot page the one thing every user of
remedik already has: **Alertmanager**.

Alertmanager's `/api/v2/alerts` takes an array of alert objects.
`webhook.call` sends one object describing the remediation, so the call is
refused with `400 parsing alerts body ... cannot unmarshal object into Go
value of type models.PostableAlerts`. Confirmed on a dev cluster, not
inferred.

That is the wrong endpoint to be unable to reach. Escalating back into
Alertmanager is the cheapest correct escalation there is, because everything
that decides *who gets woken* already lives there:

- the routing tree, so the page reaches whoever owns that namespace
- silences, so a maintenance window suppresses remedik's page too
- inhibition, so a page about a symptom is dropped while the cause is firing
- the on-call schedule, via whatever receiver the org already configured

Without it, every remedik install has to duplicate that configuration — a
second copy of the routing, a second set of credentials, and a second thing
to keep in step. Duplicated paging configuration is how a page ends up going
to somebody who left.

## What Changes

- **A `format` parameter on `webhook.call`.** `format: alertmanager` sends
  the same facts as an Alertmanager `PostableAlerts` array instead of a bare
  object. `format: remedik`, the default, is today's body unchanged.
- **A cookbook recipe** that escalates back into Alertmanager, and says what
  the alert it raises is named and labelled, because a page whose labels do
  not route is a page nobody receives.

## Non-goals

- **A body template.** The webhook body explains itself with no templating,
  deliberately — a strategy is a thing people read at 3am, and a Go template
  in it is a second language to debug during an incident. `format` is a
  named shape, not a template, and there will be a small number of them.
- **Resolving remedik's alert when the remediation later succeeds.** A
  remediation is not retried, so nothing here would ever send the resolve. It
  belongs with the alert whose firing state Alertmanager already tracks.
- **A dedicated `alertmanager.notify` action.** It would need its own RBAC
  story, its own tests and its own place in the registry to do what one
  parameter does. The action is still "call an HTTP endpoint".

## Capabilities

### Modified Capabilities

- `action-webhook-call`

## Impact

- `internal/action/external/webhook.go`: one parameter, one body shape.
- `examples/strategies/`: one recipe.
- No new RBAC. No new reach: the endpoint is still whatever the step names.
