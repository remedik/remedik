# remedik

> Predictably boring auto-remediation for Kubernetes alerts.

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
```

```console
$ kubectl get remediations -n remedik
NAME                  STRATEGY        TARGET                    STATE       AGE
pod-crashloop-x7k2q   pod-crashloop   deployment/payments/api   Succeeded   2m
pod-crashloop-b91mm   pod-crashloop   deployment/checkout/web   Simulated   1h
```

**Four actions ship today**, each a separate permission the chart grants only
when you enable it:

| Action | What it does |
| --- | --- |
| `deployment.restart` | Rolling restart of a Deployment |
| `workload.restart` | The same, for StatefulSets and DaemonSets too |
| `pod.delete` | Evicts one pod **through the Eviction API**, so a PodDisruptionBudget can refuse it |
| `job.delete` | Deletes a failed Job so its CronJob makes a clean run |

The eviction detail is not a detail. Deleting a pod ignores disruption
budgets entirely; eviction is the only call that checks them. remedik cannot
delete a pod even if it wanted to — the permission it holds is `create` on
`pods/eviction`, never `delete` on `pods` — and it refuses a pod with no
controller owner, because nothing would recreate it.

There is also a read-only dashboard, off by default, that answers the same
questions in a browser — how much a dry-run trial would have done, and why
nothing happened during an incident.

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
patch Deployments.

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
  `deployment.restart`, `workload.restart`, `pod.delete`, `job.delete`,
  read-only dashboard, Helm chart, Prometheus metrics, signed releases.
- **v0.2.0** — Slack bot with approval buttons and manual commands, more
  built-in actions, custom actions (`job`, `script`), audit sinks (Splunk
  HEC, Loki, Elasticsearch), namespace health.
- **Later** — hub/spoke multi-cluster, cloud packs, `ActionPlugin` CRD, MCP
  server, workload-aware cost recommendations.

## License

[Apache-2.0](LICENSE)
