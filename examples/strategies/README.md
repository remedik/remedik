# Cookbook

Each recipe is a `RemediationStrategy` you can apply as-is, with the
reasoning behind its guards written down. Copy one, change the matchers to
suit your alerts, and start with the operator in dry-run.

| Recipe | Alert | What it does |
| --- | --- | --- |
| [pod-crashloop.yaml](pod-crashloop.yaml) | `KubePodCrashLooping` | Rolling restart of the Deployment |
| [oom-restart.yaml](oom-restart.yaml) | `KubeContainerOOMKilled` | Restart, cautiously and rarely |
| [scoped-to-one-namespace.yaml](scoped-to-one-namespace.yaml) | `KubePodCrashLooping` in one namespace | Narrower rule that wins over a broad one |
| [statefulset-stuck.yaml](statefulset-stuck.yaml) | `KubeStatefulSetUpdateNotRolledOut` | Rolling restart of a StatefulSet |
| [pod-not-ready.yaml](pod-not-ready.yaml) | `KubePodNotReady` | Evicts one pod, honouring PodDisruptionBudgets |
| [job-failed.yaml](job-failed.yaml) | `KubeJobFailed` | Deletes the Job so its CronJob runs again |
| [escalate-to-pipeline.yaml](escalate-to-pipeline.yaml) | `KubeNodeNotReady` | Hands the incident to a pipeline outside the cluster |
| [run-a-runbook.yaml](run-a-runbook.yaml) | `KubePodCrashLooping` | Runs a script from a ConfigMap as a Job |
| [bounded-eviction.yaml](bounded-eviction.yaml) | `KubePodNotReady` | Evicts a pod, but never the last healthy replica |
| [rollback-a-bad-deploy.yaml](rollback-a-bad-deploy.yaml) | `KubeDeploymentReplicasMismatch` | Puts the previous revision back; refuses under GitOps |
| [hpa-maxed-out.yaml](hpa-maxed-out.yaml) | `KubeHpaMaxedOut` | Raises the autoscaler's ceiling |
| [node-not-ready.yaml](node-not-ready.yaml) | `KubeNodeNotReady` | Cordons the node — reversible, moves nothing |
| [drain-a-dead-node.yaml](drain-a-dead-node.yaml) | `KubeNodeUnreachable` | Drains it, honouring disruption budgets |
| [volume-filling-up.yaml](volume-filling-up.yaml) | `KubePersistentVolumeFillingUp` | Expands the claim, where the class allows |
| [escalate-on-failure.yaml](escalate-on-failure.yaml) | `KubePodCrashLooping`, `KubeNodeNotReady` | Remediates, and pages somebody when that does not work |
| [give-up-and-say-so.yaml](give-up-and-say-so.yaml) | `KubePodCrashLooping` | Stops remediating a workload that keeps breaking, and pages, because the restarts were succeeding and the problem was not |
| [escalate-into-alertmanager.yaml](escalate-into-alertmanager.yaml) | `KubePodCrashLooping` | Raises `RemediationFailed` in Alertmanager, so the routing you already have decides who is woken |
| [replace-a-node.yaml](replace-a-node.yaml) | `KubeNodeUnreachable` | Drains it here, lets a pipeline terminate the instance |

Every action a recipe uses must be enabled in the chart, because each one is
a permission: `--set actions.podDelete.enabled=true`. The chart's
`action-rbac.yaml` lists what each is allowed to touch, and why.

## Choosing guards

**`cooldown`** answers "how long before trying the same thing on the same
target again?". Set it longer than the time it takes to know whether the
remediation worked — a rollout plus a couple of scrape intervals. Too short
and a flapping alert becomes a restart loop.

**`blastRadius`** answers "is this workload already too broken to touch?".
The other two are about time; this one is about state, and it is what bounds
the actions that remove capacity rather than replacing it. `minAvailable: 1`
is the rule almost everyone wants and almost nobody writes down.

It reads the workload, so it needs `guards.blastRadius.enabled=true` in the
chart — and **refuses rather than allows** when it cannot read: a guard that
permits an execution it could not evaluate is not a guard.

**`maxPerHour`** answers "how bad can an alert storm get?". Think about how
many of these you would be willing to see happen unattended in an hour. It
is the difference between one noisy workload and a cluster-wide event.

**`retries`** is for transient failures — a conflict, a momentarily
unavailable API server. It is not for "it did not fix the problem": if the
first restart did not help, a second one rarely does, and a human should
look.

## Which strategy wins

When several strategies match an alert, the most specific one runs — the one
with the most matchers. Ties are broken by name, so the outcome never
depends on the order you applied them. That is what makes
`scoped-to-one-namespace.yaml` a safe override of a broad default.
