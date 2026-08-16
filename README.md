<p align="center">
  <img src="docs/brand/remedik-banner.png" alt="remedik — Kubernetes auto-remediation operator" width="640">
</p>

# remedik

> Predictably boring auto-remediation for Kubernetes alerts.

[![CI](https://github.com/remedik/remedik/actions/workflows/ci.yml/badge.svg)](https://github.com/remedik/remedik/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/remedik/remedik)](https://goreportcard.com/report/github.com/remedik/remedik)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

remedik turns Alertmanager alerts into safe, auditable remediation.
Strategies are custom resources you keep in git, every execution is recorded
as a `Remediation` object, guards bound the blast radius, and an LLM never
sits in the execution path.

**Status: alpha.** The core loop works end to end — alert in, guarded
remediation out, audit trail in the cluster — and is covered by unit tests
and an end-to-end test on kind. The API may still change; every change is
specified in [`openspec/`](openspec/) before it lands.

## What it does today

```yaml
apiVersion: remedik.dev/v1alpha1
kind: RemediationStrategy
metadata:
  name: pod-crashloop
spec:
  trigger:
    match:
      alertname: KubePodCrashLooping
  guards:
    cooldown: 15m        # do not restart the same workload again this soon
    maxPerHour: 4        # an alert storm cannot amplify into an outage
  steps:
    - action: deployment.restart
  onFailure:
    retries: 1
    steps:                        # and if that still does not work, page somebody
      - action: webhook.call
        with:
          url: https://events.pagerduty.com/v2/enqueue
          secretRef: pagerduty-routing-key
          secretKey: key
```

```console
$ kubectl get remediations -n remedik
NAME                  STRATEGY        TARGET                    STATE       AGE
pod-crashloop-x7k2q   pod-crashloop   deployment/payments/api   Succeeded   2m
pod-crashloop-b91mm   pod-crashloop   deployment/checkout/web   Simulated   1h
```

**Fourteen actions ship today**, each a separate permission the chart grants
only when you enable it, and only `deployment.restart` on by default:

| | Action | What it does |
| --- | --- | --- |
| **Workloads** | `deployment.restart` | Rolling restart of a Deployment |
| | `workload.restart` | The same, for StatefulSets and DaemonSets too |
| | `pod.delete` | Evicts one pod **through the Eviction API**, so a PodDisruptionBudget can refuse |
| | `job.delete` | Deletes a failed Job so its CronJob makes a clean run |
| **Capacity** | `deployment.rollback` | Puts the previous revision back — refuses under Argo CD or Flux |
| | `deployment.scale` | Sets or increases replicas — refuses when an HPA owns the workload |
| | `hpa.scale` | Raises an autoscaler's ceiling; never lowers it |
| **Nodes** | `node.cordon` / `node.uncordon` | Stop and resume scheduling. Reversible, moves nothing |
| | `node.drain` | Cordon, then evict everything, honouring disruption budgets |
| | `pvc.expand` | Grows a volume — only where the StorageClass allows it |
| **Anything else** | `webhook.call` | POSTs the incident to your pipeline |
| | `job.run` / `script.run` | Runs your container or your runbook script as a Job |

Several of those descriptions say "refuses", and that is the interesting
part. Deleting a pod ignores disruption budgets entirely; eviction is the
only call that checks them, so remedik holds `create` on `pods/eviction` and
never `delete` on `pods` — it *cannot* force a pod out. A rollback in a
GitOps cluster would be reverted within minutes, so it refuses rather than
recording a success while the outage continues. A volume whose StorageClass
forbids expansion accepts the patch and does nothing, so remedik checks
first. And a remediation Job runs as a ServiceAccount you name, never
remedik's own, which is refused.

**When remediation does not work, somebody gets told.** `onFailure.steps` is
a second plan that runs once the retries are spent, so the loop closes:

```
alert --> remedik --> remediate --> it worked, done
                              \--> it did not --> page whoever is on call
```

Escalating is not a notification setting — it is made of the same actions,
under the same RBAC, in the same audit trail. It never turns a failure into
a success, it is never retried during an incident, and it runs during a dry
run too — the one exception in remedik, so a trial proves the path before
anybody needs it. `remedik_escalations_total{outcome="Failed"}` is its own
alertable signal: a remediation failed and nobody was told.

**Posture is per namespace, so adoption is not all-or-nothing.** `dryRun` is
the default; `namespacePosture` overrides it for the namespaces that have
earned it, in one install:

```yaml
dryRun: true              # report everywhere
namespacePosture:
  staging: live           # ...except act in staging
```

It works in the other direction too — live by default, `prod: dryRun` — and
the namespace consulted is the *workload's*, not remedik's. The posture is
resolved once when the record is created and written onto it, so every
`Remediation` says which posture it ran under and an in-flight execution
keeps the one it started with.

There is also a read-only dashboard, off by default. Its front page answers
"is anything wrong right now?" — posture, what needs attention, activity
over the last day, and where remediation is happening — and each panel links
into the list behind it. `/remediations` is that list, filtered by
namespace, strategy or state. Every filter control is a link, so a narrowed
view is a URL you can paste to whoever is on call, and it works with
JavaScript off.

## Try it in five minutes

Needs Docker, [kind](https://kind.sigs.k8s.io/), kubectl and helm.

```bash
make e2e     # throwaway cluster, real image, the whole loop, then cleanup
```

That test is the honest demo. On a real cluster it proves: an
unauthenticated delivery is refused; a dry run records a plan without
touching anything; turning dry-run off actually restarts the Deployment, and
the record confirms the rollout rather than the patch; the workload carries
events explaining the change; the cooldown refuses an immediate repeat and
survives an operator restart; an unmatched alert is accepted and ignored; a
StatefulSet is restarted and a pod evicted; a pod nothing owns is refused;
and every dashboard page renders read-only.

To keep the cluster and poke at it yourself:

```bash
make dev-up        # kind + Prometheus, Alertmanager and Grafana
make dev-deploy    # build, load and install remedik (dry-run on)
kubectl -n remedik get remediations -w
```

The dashboard, enabled:

```bash
helm upgrade remedik ... --set dashboard.enabled=true \
  --set dashboard.auth.token="$(openssl rand -hex 24)"
kubectl -n remedik port-forward svc/remedik-dashboard 8082:8082
```

It serves GET and HEAD and nothing else, and enabling it grants remedik no
permission it did not already have.

## Design pillars

**The execution path is deterministic.** YAML decides, guards bound, and —
from v0.2.0 — humans approve destructive steps. Optional AI features read
and explain; they never execute. See
[ADR-0003](docs/adr/0003-deterministic-core-ai-read-only.md).

**Dry-run is a guarantee, not a flag.** Every action implements `Resolve`,
`Plan` and `Execute` separately. In dry-run the engine calls `Plan`, so the
mutating code path is never reached — a `Simulated` record cannot have
touched the cluster even if an action is buggy.

**Minimal trust.** One agent per cluster, RBAC generated only for the
actions you enable, distroless non-root image, no external orchestrator and
no database. Turning off `deployment.restart` removes its permission to
patch Deployments — and `make helm-lint` checks that with every action off,
nothing at all is granted on any workload.

**Guards bound the damage.** `cooldown` and `maxPerHour` ask about time;
`blastRadius` asks about state — never the last available replica, never a
workload already too degraded to touch. It **fails closed**: a guard that
permits an execution when it could not evaluate its own condition is not a
guard.

**The audit trail is a first-class object.** Every run — including dry-run
simulations — is a `Remediation` resource carrying the triggering alert, the
plan, per-step outcomes and timings. It keeps its own copy of the plan, so
the record still explains the run after the strategy is edited or deleted.

## Documentation

| Doc | Purpose |
| --- | --- |
| [QUICKSTART.md](QUICKSTART.md) | Install it, or work on it |
| [docs/architecture.md](docs/architecture.md) | Components, state machine, guards, topologies |
| [docs/advanced-setup.md](docs/advanced-setup.md) | Hub/spoke, cloud packs, audit sinks, AI diagnosis (planned) |
| [charts/remedik/README.md](charts/remedik/README.md) | Every chart value |
| [examples/strategies/](examples/strategies/) | Cookbook |
| [docs/adr/](docs/adr/) | Why things are the way they are |
| [SECURITY.md](SECURITY.md) | Security policy and commitments |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Spec-first workflow |
| [CLAUDE.md](CLAUDE.md) | Orientation for AI assistants working on the repo |

## Roadmap

- **v0.1.0 (in progress)** — alert gateway, `RemediationStrategy` and
  `Remediation` CRDs, deterministic engine with guards, dry-run and retries,
  fourteen actions across workloads, capacity, nodes and escape hatches,
  three guards including `blastRadius`, escalation through `onFailure.steps`,
  per-namespace posture, a filterable read-only dashboard, a Helm chart whose RBAC follows the features you
  enable, Prometheus metrics with a Grafana dashboard and alerts, signed
  releases.
- **v0.2.0** — the Slack bot with approval buttons and manual commands,
  namespace health, audit sinks (Splunk HEC, Loki, Elasticsearch).
- **Later** — hub/spoke multi-cluster, cloud packs, `ActionPlugin` CRD, MCP
  server, workload-aware cost recommendations.

## License

[Apache-2.0](LICENSE)
