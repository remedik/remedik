# Why did nothing happen?

The question remedik gets asked most, and the one a tool like this has to answer
well: it is *supposed* to do very little, so "nothing happened" is both the
normal case and the failure case, and telling them apart is the whole job.

Everything below is a command you can run and a line you can read. Nothing here
needs access to the operator's source.

---

## Turn on the log that explains decisions

```bash
helm upgrade remedik ... --set logLevel=debug
```

At `info`, remedik logs one line per decision. At `debug` it also explains the
decisions it did **not** take — most usefully, why each strategy was not the one:

```
INFO   no strategy matches this alert
       labels=alertname=KubeDeploymentReplicasMismatch deployment=api namespace=payments severity=warning
DEBUG  a strategy did not match  strategy=pod-crashloop
       why=alertname is KubeDeploymentReplicasMismatch, the strategy wants KubePodCrashLooping
DEBUG  a strategy did not match  strategy=drain-safely
       why=alertname is KubeDeploymentReplicasMismatch, the strategy wants KubeNodeNotReady
```

That is one line per strategy per unmatched alert, so it is off by default. Turn
it on while you are debugging and turn it back off.

```bash
kubectl -n remedik logs deploy/remedik -f
```

---

## Follow the alert through

An alert passes six gates. Walk them in order; the first one that says no is
your answer.

### 1. Did the alert arrive at all?

```bash
kubectl -n remedik port-forward svc/remedik-metrics 8080:8080
curl -s localhost:8080/metrics | grep remedik_alerts
```

`remedik_alerts_received_total` not moving means Alertmanager is not delivering.
Check its own logs and that its route names remedik's receiver — a receiver
defined but never routed to is the most common cause, and Alertmanager reports
that as healthy.

The gateway's answers are worth knowing, because they are deliberate:

| | |
| --- | --- |
| **200** | understood — *including* "no strategy matched". Alertmanager retries a non-2xx, so a normal outcome must not look like a failure |
| **401** | the bearer token does not match. `remedik_unauthorized_total` counts these |
| **400** | the body did not parse as an Alertmanager delivery |
| **413** | the delivery was larger than the body limit |
| **503** | this replica does not hold the lease, so it is not the one acting |

### 2. Did a strategy match?

```bash
kubectl -n remedik logs deploy/remedik | grep "no strategy matches"
```

With `logLevel=debug` the next lines name every strategy and what it wanted. The
cause is nearly always a label:

- **the label is not the one you think.** `kube-state-metrics` alerts often carry
  `exported_namespace` rather than `namespace` when Prometheus relabels a
  scrape's own namespace onto it.
- **the value has whitespace.** `namespace: payments ` and `namespace: payments`
  look identical in a YAML file and are two different strings.
- **matching is equality only, never a regex.** This is deliberate: an operator
  woken at 03:00 has to be able to predict what will match.
- **the strategy is disabled**, which the debug line says first.

### 3. Was the target resolvable?

```bash
kubectl -n remedik get remediations
kubectl -n remedik describe remediation <name>
```

A record that exists but failed immediately usually says so precisely:

```
resolve target for deployment.restart: no namespace: the alert has no
"namespace" label and the step sets no "namespace" parameter
```

Either add the label at the alert, or name it on the step:

```yaml
steps:
  - action: deployment.restart
    with:
      namespace: payments
```

### 4. Did a guard refuse?

Guard refusals are events on the strategy, so they are where anybody would look:

```bash
kubectl get events --field-selector reason=GuardRejected --sort-by=.lastTimestamp
```

```
refused KubePodCrashLooping[firing] api: guard "cooldown":
pod-crashloop completed on deployment/payments/api 1s ago, cooldown is 15m0s
```

Four guards can say no, and each says which one it was:

| Guard | Refuses because |
| --- | --- |
| `cooldown` | the same target was remediated too recently |
| `maxPerHour` | an alert storm would otherwise amplify into an outage |
| `blastRadius` | the workload is already too degraded to touch, or this is the last available replica |
| `giveUpAfter` | this alert has been remediated repeatedly and is not getting better, so remedik stopped and escalated instead |

`remedik_guard_rejections_total{guard="..."}` is the same information over time,
and the Grafana dashboard charts it.

A guard that cannot evaluate its own condition **refuses**. A guard that permits
an execution when it does not know is not a guard.

### 5. Is remedik allowed to act here?

```bash
kubectl -n remedik get remediations -o custom-columns=\
NAME:.metadata.name,STATE:.status.state,DRYRUN:.spec.dryRun
```

`Simulated` and `dryRun: true` mean remedik did the work and changed nothing,
which is the install default. The posture is per namespace and the namespace that
counts is the *workload's*, not remedik's:

```yaml
dryRun: true
namespacePosture:
  payments: live
```

The posture is resolved once, when the record is created, and written onto it —
so an old record showing `Simulated` is history, not the current setting. The
dashboard's `/namespaces` page and `remedik_namespace_posture` both answer "where
does it act" without reading a values file.

### 6. Is it paused, or waiting for a person?

```bash
kubectl -n remedik get configmap remedik-pause -o yaml
```

`paused: "true"` forces dry-run on every replica. Records still appear, marked
`Simulated` and labelled with the reason, so nothing about what would have
happened is lost.

```bash
kubectl -n remedik get remediations \
  -o jsonpath='{range .items[?(@.status.state=="AwaitingApproval")]}{.metadata.name}{"\n"}{end}'
```

(`--field-selector` cannot do this: a custom resource's own status fields are not
indexed for it unless the CRD declares them, which this one does not yet.)

Those are waiting for a human, and they escalate when their deadline passes:

```bash
kubectl -n remedik patch remediation <name> --type merge \
  -p '{"spec":{"approval":{"decision":"approve","by":"your-name"}}}'
```

A strategy in `mode: manual` never starts from an alert at all, and says so as an
event on the strategy.

---

## What a terminal reason means

`status.reason` on a `Remediation`:

| Reason | What happened |
| --- | --- |
| `StepFailed` | a step failed after its retries; `status.steps` says which and why |
| `UnknownAction` | the strategy names an action this build does not have enabled. `helm get values` and the `actions` block are the answer |
| `Interrupted` | the process died mid-execution. remedik **never resumes** a half-finished attempt — repeating a mutating step is the worse outcome — so it is failed and left for a person. A record waiting for a retry is `Pending`, never `Running` |
| `GuardRejected` | a guard refused before anything ran |
| `GaveUp` | this alert has been remediated repeatedly without the underlying problem improving. remedik stopped and escalated; the message names the count and window |
| `ApprovalTimeout` | nobody approved or denied in time, so it escalated rather than running |
| `Denied` | somebody said no. Deliberately no escalation: they looked |
| `ManualStrategy` | the strategy is manual, so the alert created nothing |

---

## A field will not apply

```
strict decoding error: unknown field "spec.execution.approvalTimeout"
```

`helm upgrade` does **not** upgrade CRDs — Helm applies a chart's CRDs once, on
first install, and never again. So a field added since your install is not there,
and the API server would otherwise prune it silently.

```bash
helm show crds oci://ghcr.io/remedik/charts/remedik | \
  kubectl apply --server-side --force-conflicts -f -
```

`--force-conflicts` is part of the command: Helm recorded itself as the field
manager when it installed them, and taking that over is the intent. Applying a
CRD does not touch the resources already using it.

remedik refuses an upgrade in this state rather than letting it happen quietly,
so in practice the chart tells you this before you hit it.

---

## Nothing was told to anybody

The signal to alert on:

```promql
sum(increase(remedik_escalations_total{outcome="Failed"}[15m])) > 0
```

A remediation failed *and* the attempt to report it failed too. Assume nobody
was told.

The other half is a failure with no escalation at all — a strategy with no
`onFailure.steps`. That is not a fault, but if you route alerts to remedik
instead of to on-call it means the alert reached nobody.
[docs/routing.md](routing.md) is about the safety net for exactly this, and it is
the section not to skip. The dashboard's front page counts both.

---

## The dashboard will not load

It is off by default. Enabled, it serves on its own port with one credential:

```bash
helm upgrade remedik ... \
  --set dashboard.enabled=true \
  --set dashboard.auth.token="$(openssl rand -hex 24)"

kubectl -n remedik port-forward svc/remedik-dashboard 8082:8082
```

The browser asks for a username and a password: **leave the username empty** and
paste the token as the password. Scripts can send `Authorization: Bearer
<token>` instead.

It serves GET and HEAD and answers 405 to everything else, so nothing on it can
change your cluster. If a control looks like it should write something, it does
not — [invariants.md](invariants.md) explains why that is structural.

---

## Still stuck

Open an issue with the output of:

```bash
kubectl -n remedik logs deploy/remedik --tail=200
kubectl -n remedik describe remediation <name>
kubectl get remediationstrategy <name> -o yaml
helm -n remedik get values remedik
```

Those four answer nearly every question anybody can ask from the outside. Please
redact your alert labels if they carry anything you would not publish — they are
the part most likely to contain a hostname or a customer name.
