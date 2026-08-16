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

## Try it in five minutes

Needs Docker, [kind](https://kind.sigs.k8s.io/), kubectl and helm.

```bash
make e2e     # throwaway cluster, real image, five assertions, then cleanup
```

That test is the honest demo: it proves an unauthenticated delivery is
refused, a dry run records a plan without touching anything, turning dry-run
off actually restarts the Deployment, the cooldown refuses an immediate
repeat, and an unmatched alert is accepted and ignored.

To keep the cluster and poke at it yourself:

```bash
make dev-up        # kind + Prometheus, Alertmanager and Grafana
make dev-deploy    # build, load and install remedik (dry-run on)
kubectl -n remedik get remediations -w
```

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
  `deployment.restart`, Helm chart, Prometheus metrics, signed releases.
- **v0.2.0** — Slack bot with approval buttons and manual commands, more
  built-in actions, custom actions (`job`, `script`), read-only GUI, audit
  sinks (Splunk HEC, Loki, Elasticsearch), namespace health.
- **Later** — hub/spoke multi-cluster, cloud packs, `ActionPlugin` CRD, MCP
  server, workload-aware cost recommendations.

## License

[Apache-2.0](LICENSE)
