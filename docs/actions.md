# Actions

Every action remedik can run, what it takes, what it checks afterwards, and
what it refuses. Then the part that matters most once you have written two
strategies: **how to make it do something nobody here thought of**, without
writing Go and without waiting for us.

The [cookbook](../examples/strategies/) is where to start — copy a recipe and
change its matchers. This page is where to come back to when the recipe does
not quite fit.

## How a step works

A step is an action and a map of strings:

```yaml
steps:
  - action: deployment.scale
    with:
      increasePercent: "50"
      max: "10"
```

Values are strings, always, including numbers and booleans. The schema stays
the same shape across every action that way, and `kubectl explain` keeps
working; the cost is a pair of quotes.

Four things happen to a step, in this order:

1. **Resolve** turns the alert into a target — a namespace and a name.
2. **Plan** works out what would change, and performs every check Execute
   performs. Dry-run stops here, which is why dry-run is a structural
   guarantee rather than a flag: the mutating path is never reached.
3. **Execute** makes the change.
4. **Verify** answers the question the operator is actually asking — *did it
   work* — rather than *was the request accepted*. Those are different, and
   most of this catalogue exists because they are.

Steps run in order and **stop at the first failure**. The rest are recorded as
`Skipped`, not as successes. That rule is what makes a check a check: see
[Actions you write yourself](#actions-you-write-yourself).

## Naming the target

Resolve reads the alert's labels. A parameter of the same name on the step
overrides the label, which is how a strategy handles an alert that does not
carry what the action needs.

| Action | Label it reads | Parameter that overrides it |
| --- | --- | --- |
| `deployment.restart`, `deployment.scale`, `deployment.rollback` | `deployment` | `name` |
| `workload.restart` | `deployment`, `statefulset` or `daemonset`, by `kind` | `name`, `kind` |
| `pod.delete` | `pod` | `pod` |
| `job.delete` | `job_name` | `job` |
| `hpa.scale` | `horizontalpodautoscaler` | `name` |
| `pvc.expand` | `persistentvolumeclaim` | `name` |
| `node.cordon`, `node.uncordon`, `node.drain` | `node` | `name` |

Every namespaced action also reads `namespace`, overridable with a `namespace`
parameter. If a managed rule package gives you `exported_namespace` instead,
that is the one case where the target and the alert disagree — the dashboard
says so in as many words on the record that failed.

## Understood by every action

| Parameter | Default | What it does |
| --- | --- | --- |
| `verifyTimeout` | `60s` | How long the post-condition check may wait. Capped at `10m`: executions are serialised, so a long check is time no other remediation can use. |

## Workloads

### `deployment.restart` — chart: `actions.deploymentRestart.enabled`

A rolling restart, by patching `kubectl.kubernetes.io/restartedAt` on the pod
template. It never deletes pods: the workload's own controller does the
rollout, so `maxUnavailable`, readiness probes and PodDisruptionBudgets all
still apply. Deleting pods would bypass all three.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `name` | the `deployment` label | Which Deployment |

**Verifies** the rollout finished — the controller's own status, after it has
observed the change — not that the patch was accepted.

### `workload.restart` — chart: `actions.workloadRestart.enabled`

The same, for StatefulSets and DaemonSets as well.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `kind` | inferred from the alert's labels | `Deployment`, `StatefulSet` or `DaemonSet` |
| `name` | the label matching the kind | Which workload |

### `pod.delete` — chart: `actions.podDelete.enabled`

Removes one pod so its controller replaces it. It **evicts** rather than
deletes, and that is the whole point: deleting ignores PodDisruptionBudgets
entirely, and the Eviction API is the only call that checks them — answering
429 when the removal would breach the budget. remedik holds `create` on
`pods/eviction` and never `delete` on `pods`, so it *cannot* force a pod out.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `pod` | the `pod` label | Which pod |
| `requireOwner` | `true` | Refuse a pod with no controller: nothing would recreate it, so evicting it is a deletion, not a remediation |
| `gracePeriodSeconds` | the pod's own | Override the termination grace period |

**Verifies** the pod is gone, or replaced by one of the same name with a
different UID — a StatefulSet reuses names. An eviction that returned 201 has
been *accepted*, not completed.

### `job.delete` — chart: `actions.jobDelete.enabled`

Deletes a failed Job so the CronJob that owns it makes a clean one. A failed
Job stays failed: nothing retries it, and its pods sit there holding their
logs and their resources until somebody notices.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `job` | the `job_name` label | Which Job |
| `propagationPolicy` | `Background` | `Background`, `Foreground` or `Orphan` |

**Verifies** the Job is actually gone. Background propagation returns as soon
as the deletion is recorded, not when it has finished.

## Capacity

### `deployment.rollback` — chart: `actions.deploymentRollback.enabled`

Puts the previous revision back. The most common cause of a crash loop at 3am
is the deploy at 2:50, which makes this the highest-value action here and the
one most likely to surprise somebody.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `name` | the `deployment` label | Which Deployment |
| `toRevision` | the previous one | A specific revision from the ReplicaSet history |
| `ignoreGitOps` | `false` | Roll back anyway under Argo CD or Flux |

**Refuses** a workload managed by Argo CD or Flux, unless `ignoreGitOps` is
set: the controller would revert the rollback within minutes, and remedik
would have recorded a success while the outage continued.

**Verifies** the rollout, exactly as a restart does — whether the old version
came back up, not whether the patch was accepted.

### `deployment.scale` — chart: `actions.deploymentScale.enabled`

| Parameter | Default | Meaning |
| --- | --- | --- |
| `replicas` | — | An absolute count |
| `increaseBy` | — | Add this many to the current count |
| `increasePercent` | — | Add this share of the current count, rounded up |
| `max` | — | The ceiling. **Required whenever the change is relative** |
| `name` | the `deployment` label | Which Deployment |

Exactly one of `replicas`, `increaseBy` and `increasePercent`. Setting two is
refused rather than resolved, because they mean different things. A relative
change with no `max` is refused too: "increase by" with no ceiling is an alert
storm with a credit card, and a default ceiling would be a number this project
invented for somebody else's cluster and budget.

**Refuses** a Deployment owned by a HorizontalPodAutoscaler — the autoscaler
reverts the change on its next interval. Use `hpa.scale` instead.

**Verifies** the new replicas are available. Requested is not running:
replicas that cannot schedule are not capacity.

### `hpa.scale` — chart: `actions.hpaScale.enabled`

Raises an autoscaler's ceiling, and never lowers it. The one useful mechanical
answer to `KubeHpaMaxedOut`: the autoscaler is pinned at its maximum and still
under pressure, so the maximum is the thing that is wrong.

Takes the same four arithmetic parameters as `deployment.scale`, applied to
`maxReplicas`, plus `name` from the `horizontalpodautoscaler` label.

## Nodes

### `node.cordon` and `node.uncordon` — chart: `actions.nodeCordon.enabled`, `actions.nodeUncordon.enabled`

Stop and resume scheduling. Cordoning is the safest action in Kubernetes and
the right first response to almost every node alert: nothing moves, nothing
restarts, new work goes elsewhere, and one command undoes it.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `name` | the `node` label | Which node |

**Verifies** by reading the node back.

### `node.drain` — chart: `actions.nodeDrain.enabled`

Cordon, then evict everything, honouring the disruption budgets the workloads
declared. The widest action remedik has, and the one most worth leaving in
dry-run for a long time.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `name` | the `node` label | Which node |
| `deleteEmptyDirData` | `false` | Evict pods with `emptyDir` volumes, whose data is lost with them |
| `evictBarePods` | `false` | Evict pods with no controller — nothing would recreate them |
| `maxPods` | — | Refuse if the node holds more than this many evictable pods |

**Verifies** the node is empty of what the drain was meant to remove.

### `pvc.expand` — chart: `actions.pvcExpand.enabled`

Grows a PersistentVolumeClaim. The whole value of the action is one check:
where the StorageClass does not set `allowVolumeExpansion`, the API server
accepts the patch and nothing happens — a remediation that reports success and
changes nothing is the worst outcome available, because nobody goes back to
look.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `name` | the `persistentvolumeclaim` label | Which claim |
| `size` | — | An absolute size, e.g. `20Gi` |
| `increasePercent` | — | Grow by a share of the current size |
| `maxSize` | — | The ceiling for a relative change |

**Verifies** the claim reports the new capacity. The request being accepted is
not the expansion happening: the CSI driver resizes the volume, and some need
the pod to restart before the filesystem follows.

## Anything else

### `webhook.call` — chart: `actions.webhookCall.enabled`

POSTs the incident somewhere. This is how an escalation reaches PagerDuty,
Alertmanager or a pipeline, and how a remediation hands off work remedik
should not be doing itself.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `url` | — | Required |
| `method` | `POST` | |
| `format` | `remedik` | `remedik`, or `alertmanager` to raise an alert your existing routing already understands |
| `secretRef` | — | A Secret **in remedik's own namespace** holding the credential |
| `secretKey` | `token` | Key inside it |
| `header` | `Authorization` | Header the credential goes in |
| `headerPrefix` | `Bearer ` | |
| `timeout` | `10s` | |
| `alertname` | `RemediationFailed` | With `format: alertmanager` |
| `severity` | `critical` | With `format: alertmanager` |

Secrets are read from remedik's namespace only. A label on an alert must never
decide which credential is used.

### `job.run` and `script.run` — chart: `actions.jobRun.enabled`, `actions.scriptRun.enabled`

The escape hatch. See below — this is the section you came for.

## Actions you write yourself

There is no plugin to compile and no pull request to wait for. `job.run` runs
any image and `script.run` runs any script, as a ServiceAccount you name, with
the incident in the environment. Anything you can express as a container is an
action.

Both are **off by default in the chart**, because each is a permission rather
than a feature.

### What the container gets

| Variable | Contents |
| --- | --- |
| `REMEDIK_REMEDIATION` | The record's name, so the script can find its own audit trail |
| `REMEDIK_STRATEGY` | The strategy that matched |
| `REMEDIK_NAMESPACE` | The target's namespace |
| `REMEDIK_TARGET` | `kind/namespace/name`, empty when the action needs no target |
| `REMEDIK_ALERT_LABELS` | Every label, as JSON |
| `REMEDIK_ALERT_<LABEL>` | One per label, uppercased |

The prefix on the per-label variables is not decoration: a label called `PATH`
would otherwise replace the container's, which is the kind of thing that
happens once and is never diagnosed.

### Parameters

| Parameter | Default | Meaning |
| --- | --- | --- |
| `image` | — | Required. `script.run` needs one too — a shell, `kubectl`, whatever the script uses |
| `command` | the image's entrypoint | **A JSON array**: `'["/bin/sh","-c","..."]'`. No shell between what you wrote and what runs, so there are no quoting rules to invent |
| `serviceAccount` | `default` | Whose authority the Job runs with |
| `configMap` | — | `script.run`: the ConfigMap holding the script |
| `key` | the only key | `script.run`: which key in it |
| `ttlSecondsAfterFinished` | `3600` | How long the finished Job is kept, so its logs survive long enough to read |
| `backoffLimit` | `0` | How many times Kubernetes retries the pod |

### The rules it enforces

- **The Job runs in remedik's own namespace.** The permission stays namespaced,
  and a strategy cannot reach into another team's.
- **It runs as the ServiceAccount the step names, never remedik's** — naming
  remedik's own is refused outright. Authority is granted deliberately or not
  at all, and a strategy author cannot inherit the operator's permissions by
  writing one word. This is invariant 7, and it is the reason this escape hatch
  is safe to leave open.
- **`default` is the default**, which usually cannot do anything. That is the
  correct failure: the step fails with a `forbidden` you can read, rather than
  succeeding with permissions nobody granted on purpose.
- **What the script prints is recorded** — the last 20 lines, up to 2KB, on the
  Remediation record. etcd is not a log store; the point is the last thing the
  script said before it stopped.

**Verifies** by waiting for the Job and recording its exit status and output. A
Job that was created is not a remediation that happened.

### Checking something before acting

There is no `if` in a strategy, and there does not need to be one. Steps stop
at the first failure, so **a check is a step that exits non-zero**:

```yaml
steps:
  # Refuse to restart during business hours in this namespace. Exit 1 and
  # nothing below runs; the record says which step stopped it and why.
  - action: script.run
    with:
      image: alpine:3.20
      serviceAccount: remedik-runbook
      configMap: business-hours
      command: '["/bin/sh","/scripts/check.sh"]'

  - action: deployment.restart
```

The record shows step 1 `Failed` with the last lines it printed, and step 2
`Skipped` — not a success, and not a silent no-op. Print the reason to stdout
and it is on the page somebody reads at 3am.

### Checking something afterwards

Every built-in action verifies its own work. A script can be that check for a
step that cannot: put it after the action, and if the thing you actually cared
about did not come true, the remediation fails, the escalation runs, and
somebody is told.

```yaml
steps:
  - action: deployment.restart
  - action: script.run          # queue drained? probe green? whatever "fixed" means here
    with:
      image: curlimages/curl:8.10.1
      serviceAccount: remedik-runbook
      command: '["/bin/sh","-c","curl -fsS http://api.$REMEDIK_ALERT_NAMESPACE.svc/healthz"]'
```

### Handing it to something that is not Kubernetes

When the fix belongs to a pipeline — replacing an instance, rotating a
credential, calling a cloud API — `webhook.call` hands the incident over
instead of asking for cloud permissions remedik deliberately does not want.
[`replace-a-node.yaml`](../examples/strategies/replace-a-node.yaml) is that
shape: drain here, terminate there.

### What is deliberately missing

A typed `ActionPlugin` — an action with its own parameter schema, validation
and declared RBAC. `job.run` already runs any image as any ServiceAccount, so a
custom action needs no code today; what is missing is the *typed* version. It
is not built because a plugin mechanism inside something holding cluster write
access is a trust surface, and guessing at its shape before seeing what people
actually ask for is how that surface ends up wider than anybody wanted.

If you build something with `script.run` that you would rather declare than
script, that is the report worth sending.

## Enabling one

An action a strategy names but the chart did not enable is not a mystery at
3am — the strategy says so, seconds after you apply it:

```console
$ kubectl get remediationstrategies
NAME       ENABLED   READY   MODE   RUNS   LAST RUN   AGE
pod-stuck            False   auto   0                 6s

$ kubectl get remediationstrategy pod-stuck \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
step 1: unknown action "pod.delete" (enabled actions: deployment.restart)
```

Each `actions.*.enabled` in the chart grants the RBAC that action needs, and
nothing else. `charts/remedik/templates/action-rbac.yaml` is the list, with the
reason for each rule beside it.
