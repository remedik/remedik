# Waking on-call only when remediation did not work

This is what remedik is for, and it is worth writing down precisely, because
the difference between the design that works and the one that looks like it
works is one alert rule.

The goal:

> An alert fires. It does **not** go to on-call. It goes to remedik, which
> tries a fixed number of times. If that works, nobody is woken. If it does
> not, on-call is paged as they always were.

Everything below is shipped and can be applied today.

## The shape

```
                      ┌─────────────────────────────────────────┐
   Prometheus         │              Alertmanager               │
   rule fires ───────▶│                                         │
   (for: 15m)         │  route: PaymentsApiDown  ──▶ remedik     │
                      │  route: RemediationFailed ──▶ on-call    │
                      │  route: PaymentsApiStuck  ──▶ on-call    │
                      └─────────────────────────────────────────┘
                                   │                    ▲
                                   ▼                    │
                              ┌─────────┐               │
                              │ remedik │───────────────┘
                              └─────────┘   raises RemediationFailed
                              tries 1..n     when it could not fix it
```

Three routes, and each exists for a reason. The third one is the one people
leave out, and it is the one that makes the rest safe to rely on.

## 1. Route the alert to remedik, not to on-call

```yaml
route:
  receiver: default-oncall
  routes:
    # The alert remedik is expected to handle. It does not continue to
    # on-call: that is the whole point.
    - receiver: remedik
      matchers:
        - alertname = "PaymentsApiDown"

receivers:
  - name: remedik
    webhook_configs:
      - url: http://remedik-gateway.remedik.svc:8090/webhooks/alertmanager
        send_resolved: false
        http_config:
          authorization:
            type: Bearer
            credentials_file: /etc/alertmanager/secrets/remedik/token
```

`send_resolved: false` because remedik acts on a problem, and a "this is over"
message is not something to act on.

Nothing about the alert changes: the same rule, the same `for: 15m`, the same
labels. Only where Alertmanager sends it.

## 2. Try, then escalate

```yaml
apiVersion: remedik.dev/v1alpha1
kind: RemediationStrategy
metadata:
  name: payments-api-restart
spec:
  enabled: true
  trigger:
    match:
      alertname: PaymentsApiDown
  guards:
    cooldown: 15m
    maxPerHour: 4
    blastRadius:
      minAvailable: 1
  steps:
    - action: deployment.restart
      with:
        deployment: api
        # This is what makes "it worked" mean something. Without it the step
        # succeeds the moment the API server accepts the patch, which says
        # nothing about whether the workload came back.
        verifyTimeout: 120s
  onFailure:
    # Three attempts in total. Paging on the first failure of a rollout that
    # is about to settle is how a page becomes noise.
    retries: 2
    steps:
      - action: webhook.call
        with:
          url: http://monitoring-kube-prometheus-alertmanager.monitoring.svc:9093/api/v2/alerts
          format: alertmanager
```

`retries: 2` means three attempts, with backoff between them. If the third
fails, `onFailure.steps` runs and raises `RemediationFailed` in Alertmanager,
carrying **every label the original alert had** — so a route you already have
on `team` or `namespace` delivers it to the same people, and your silences and
inhibition rules apply. No second copy of the paging configuration.

### Give the page a fallback

One channel is one point of failure. Escalation steps are alternative ways to
reach a person, and every one of them runs — a failed channel does not silence
the ones after it, and the escalation is recorded as `Succeeded` when any of
them got through:

```yaml
  onFailure:
    retries: 2
    steps:
      # Preferred: your own routing decides who is woken.
      - action: webhook.call
        with:
          url: http://monitoring-kube-prometheus-alertmanager.monitoring.svc:9093/api/v2/alerts
          format: alertmanager
      # If Alertmanager itself is the thing that is broken.
      - action: webhook.call
        with:
          url: https://events.pagerduty.com/v2/enqueue
          secretRef: pagerduty-routing-key
          secretKey: key
          header: Authorization
          headerPrefix: "Token token="
```

That pages twice when both work, which is the point of the second channel and
the cost of it. If you would rather try them in order and stop at the first
that gets through, add `mode: firstSuccess` beside `retries`. Either way the
record lists every channel with its own outcome, so a quietly broken one is
visible before it is needed.

Then route it:

```yaml
    - receiver: default-oncall
      matchers:
        - alertname = "RemediationFailed"
```

Make sure that receiver is **not** remedik. A strategy matching
`RemediationFailed` would have remedik remediate its own page.

### What counts as "it did not work"

remedik verifies rather than assumes, which is why this design is trustworthy:

| What happened | remedik's verdict |
| --- | --- |
| The patch was rejected | Failed → escalates |
| The rollout never completed within `verifyTimeout` | Failed → escalates |
| The pods came back and are ready | Succeeded → nobody woken |
| One escalation channel was down, another worked | Somebody was told; the broken channel is recorded |
| Every escalation channel was down | `RemedikEscalationFailing` — assume nobody was told |
| A guard refused (cooldown, rate, blast radius) | No record, no escalation — see below |
| remedik itself is down | Nothing happens — see below |

The last two rows are why step 3 exists.

## 3. Keep a safety net that does not depend on remedik

**This is the step to not skip.** Routing an alert exclusively to remedik makes
remedik part of your paging path. If it is down, wedged, or its escalation
cannot reach Alertmanager, the alert goes to a receiver that answers nobody.

There are also failure modes remedik cannot report, because from its own point
of view it did the right thing:

- **The remediation worked, and the problem came back.** The rollout completed,
  pods were ready, the record says `Succeeded`. Ten minutes later it breaks
  again. The alert re-fires, and the **cooldown correctly refuses** — a crash
  loop that survived one restart needs a person, not a second restart. But a
  refusal creates no `Remediation` and runs no escalation. Nobody is told.
- **remedik was never asked.** A misrouted alert, a bad token, a network
  policy. remedik cannot report an alert it did not receive.

The fix is one rule, and it covers all of them at once, because it does not ask
remedik anything — it asks whether the problem is still there:

```yaml
- alert: PaymentsApiStuck
  # The same condition as PaymentsApiDown, with a longer fuse.
  expr: <the same expression>
  for: 45m
  labels:
    severity: critical
  annotations:
    summary: payments/api is still down 45 minutes on
    description: >-
      This condition has held for 45 minutes. remedik was given it at 15
      minutes and either could not fix it, was refused by its own guards, or
      never received it. Handle it as you would have without remedik.
```

Routed straight to on-call. If remediation works, the condition clears before
45 minutes and this never fires. If anything at all goes wrong — including
remedik being absent — on-call is paged half an hour later than they would have
been, instead of not at all.

That is the trade being made, and it should be made deliberately: **remediation
buys you the pages that resolve themselves, at the cost of thirty minutes on
the ones that do not.** Choose the second `for:` accordingly. For something
where thirty minutes is unacceptable, route to on-call and to remedik at the
same time with `continue: true`, and accept being woken for problems that fix
themselves.

## 4. Watch remedik with the monitoring it consumes

The chart ships a `PrometheusRule` — `prometheusRule.enabled=true`. Two of its
alerts matter for this design specifically:

- **`RemedikDown`** — nothing is scraping `remedik_build_info`. If alerts are
  routed to remedik, this is a paging alert, and it must go to on-call rather
  than through remedik.
- **`RemedikGuardRefusingRepeatedly`** — a guard has refused the same strategy
  at least six times in an hour. Each refusal is correct on its own; this many
  means the alert keeps arriving and remediation is not resolving it, and
  because a refusal escalates nothing, this is the only signal that happens.
- **`RemedikEscalationFailing`** — a remediation failed *and* the page failed.
  Assume nobody was told.

## Trying it before trusting it

Two ways, and both are cheap:

**Dry run.** The install default is `dryRun: true`, and per-namespace posture
lets you go live where remediation has been earned and keep reporting
everywhere else. A dry-run remediation records the exact plan and changes
nothing — and **the escalation still runs**, which is the only thing in
remedik that runs for real during a dry run. So the whole path, including the
page, is provable before it is needed.

**On a kind cluster.** `./hack/try.sh` (or `make dev-up && make dev-deploy`) wires Alertmanager to
the gateway, so the path above is the path the dev cluster uses.

## Checklist

- [ ] The alert routes to remedik and **not** to on-call
- [ ] The strategy sets `verifyTimeout`, so "it worked" means the workload came back
- [ ] `onFailure.steps` raises `RemediationFailed` with `format: alertmanager`
- [ ] `RemediationFailed` routes to on-call, and that receiver is not remedik
- [ ] A second, longer-fuse alert routes to on-call independently of remedik
- [ ] `prometheusRule.enabled=true`, and `RemedikDown` pages without going through remedik
- [ ] Proved in dry-run before the posture was changed
