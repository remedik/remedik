## Context

`webhook.call` posts a JSON object describing the remediation. Every service
it was written for accepts an arbitrary body — PagerDuty Events v2, Slack,
a pipeline trigger — so one shape was enough.

Alertmanager is the exception, and it is the endpoint that matters most:
`POST /api/v2/alerts` takes an array of `{labels, annotations, startsAt}`,
and refuses anything else with a 400.

## Decisions

### 1. A named format, not a template

The obvious fix is a body template. It is the wrong one. A strategy is read
by somebody at 3am who did not write it, and a Go template inside it is a
second language to debug at exactly the wrong moment. Worse, a template makes
every body possible, which means the action can no longer state what it
sends — and "what does this actually POST" is the question a reviewer asks
before granting an operator webhook access.

`format` is a closed set. There are two shapes now and there will be a small
number ever; each one is code somebody can read.

### 2. The labels are what make the page route, so they are the design

An alert Alertmanager cannot route is an alert nobody receives. The raised
alert therefore carries:

- `alertname: RemediationFailed` — the name a receiver matches on, and one
  that says what happened rather than repeating the original alert's name.
  Reusing the original name would collide with the still-firing alert that
  caused this, and Alertmanager would treat them as the same alert.
- `severity: critical` — remediation was attempted and did not work, which
  is worse than the symptom that triggered it. Overridable.
- **the original alert's labels**, so the routing tree that sent the symptom
  to a team sends the failure to the same team. This is the whole point.
- remedik's own `remedik_*` labels, so a receiver can show which remediation
  and which target without a lookup.

`annotations` carry the prose: the failure message and the equivalent
`kubectl`, which is what somebody woken up actually wants.

### 3. `startsAt` is set, `endsAt` is not

Alertmanager expires an alert it stops hearing about after `resolve_timeout`.
That is the correct behaviour here: remedik pages once and is not retried, so
an alert that never expires would need somebody to resolve it by hand.
Setting `endsAt` would resolve it immediately, which is worse than not
sending it.

### 4. The 400 is the reason this is a change and not a doc fix

It would be possible to document "wrap your own array with a sidecar". That
is a workaround for a one-line gap in an action whose whole purpose is
reaching things remedik does not implement.

## Risks / Trade-offs

- **A page that loops.** If remedik has a strategy matching
  `RemediationFailed`, its own page becomes an alert it remediates. The
  recipe says so and the guards bound it, but the honest mitigation is not
  writing that strategy.
- **`format` invites more formats.** Each one is code and a test rather than
  a config knob, which is the point: the cost of adding one is visible.
