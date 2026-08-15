# remedik

> Predictably boring auto-remediation for Kubernetes alerts.

**Status: pre-alpha — scaffolding.** Nothing is installable yet. The MVP core
is fully specified in
[`openspec/changes/add-mvp-core`](openspec/changes/add-mvp-core/proposal.md)
and is the next thing that lands.

remedik is a Kubernetes operator that turns Alertmanager alerts into safe,
auditable remediation: strategies are CRDs you keep in git, every execution
is recorded as a `Remediation` resource, destructive actions ask a human on
Slack first, and an LLM never sits in the execution loop.

## Why

Every platform team eventually wires Alertmanager → some runner → scripts
that restart deployments and drain nodes, then bolts on gates, cooldowns,
retries and escalation. remedik packages that battle-tested loop as a single
`helm install` — in-cluster, no external orchestrator, no database.

## How it will work (target UX)

```yaml
apiVersion: remedik.dev/v1alpha1
kind: RemediationStrategy
metadata:
  name: pod-crashloop
spec:
  trigger:
    match:
      alertname: KubePodCrashLooping
  execution:
    mode: auto            # auto | approval | manual
  guards:
    cooldown: 15m
    maxPerHour: 4
  steps:
    - action: deployment.restart
  onFailure:
    retries: 1
```

```console
$ kubectl get remediations
NAME                  STRATEGY        TARGET            STATE       AGE
pod-crashloop-x7k2q   pod-crashloop   deploy/payments   Succeeded   2m
```

## Design pillars

1. **Deterministic core** — YAML decides, guards bound the blast radius,
   humans approve destructive steps. AI (optional, bring-your-own-LLM) only
   reads and explains; it never executes.
2. **In-cluster, minimal trust** — one agent per cluster by default,
   feature-scoped RBAC, no external orchestrator, no database.
3. **Audit is a first-class object** — every run (including dry-run
   simulations) is a CR; export to Splunk/Loki/Elastic; dry-run produces
   "what I would have done" reports.
4. **Spec-driven** — no feature without an approved spec in
   [`openspec/`](openspec/). See [CONTRIBUTING.md](CONTRIBUTING.md).

## Documentation

| Doc | Purpose |
| --- | --- |
| [QUICKSTART.md](QUICKSTART.md) | Developer quickstart (works today) + target user quickstart (v0.1.0) |
| [docs/architecture.md](docs/architecture.md) | Components, execution modes, guards, topologies |
| [docs/advanced-setup.md](docs/advanced-setup.md) | Hub/spoke, cloud packs, audit sinks, AI diagnosis (planned) |
| [docs/adr/](docs/adr/) | Architecture decision records |
| [SECURITY.md](SECURITY.md) | Security policy and commitments |

## Roadmap (abridged)

- **v0.1.0** — alert gateway, `RemediationStrategy`/`Remediation` CRDs,
  deterministic engine with guards and dry-run, `deployment.restart`,
  working Helm chart, signed releases.
- **v0.2.0** — Slack bot (Socket Mode) with approval buttons and manual
  commands, more built-in actions, custom actions (`job`, `script`),
  read-only GUI, audit sinks (Splunk HEC, Loki, Elastic), namespace health.
- **Later** — hub/spoke multi-cluster, cloud packs, `ActionPlugin` CRD,
  MCP server, workload-aware cost recommendations.

## License

[Apache-2.0](LICENSE)
