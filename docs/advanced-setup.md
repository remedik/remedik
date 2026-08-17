# Advanced setup

> **Everything on this page is planned, not shipped**, and the chart values for
> it are not shipped either: a key that quietly does nothing is worse than a
> missing one, because somebody sets it, believes it, and finds out during an
> incident. The YAML below is a design, not a configuration you can apply.
>
> Each section is a design commitment. It becomes concrete, tested
> documentation when the capability lands. For what works today see
> [QUICKSTART.md](../QUICKSTART.md), [troubleshooting](troubleshooting.md) and
> the generated [chart reference](../charts/remedik/README.md).

## Notifications

`execution.mode` is **shipped**: `auto`, `approval` and `manual` all work, and
approving is a `kubectl patch` on the remediation — see
[QUICKSTART](../QUICKSTART.md) and
[architecture](architecture.md#execution-modes). What is still planned is
`notify.level` (`none` / `onCompletion` / `verbose`) and the Slack bot that
would carry it, which is a nicer front end for the gate rather than the thing
that brings it.

## Install profiles

Not implemented; the chart currently installs one shape, with individual
features toggled by their own values (`actions.*`, `gateway.*`). Profiles
would bundle those into three defaults:

```yaml
profile: standard   # minimal | standard | full
```

- `minimal` — gateway + engine + CRDs.
- `standard` — + Slack bot, metrics, bundled Grafana dashboard.
- `full` — + pipelines area (cloud packs + runbook runner) and GUI; for
  greenfield clusters, an optional kube-prometheus-stack subchart.

## Hub/spoke (multi-cluster)

One install on a management cluster (`mode: hub`); spokes are registered,
not installed:

```yaml
apiVersion: remedik.dev/v1alpha1
kind: ManagedCluster
metadata:
  name: prod-eu
spec:
  kubeconfigSecretRef: cluster-prod-eu   # token from the bootstrap manifest
  remediation: enabled                   # enabled | dryRun | off
  packs: [core, aws-nodes]
  slack:
    channel: "#ops-prod-eu"
```

Onboarding a spoke: `kubectl apply -f bootstrap.yaml` on the target (creates
only a minimal ServiceAccount + Role + token — nothing runs there), then
register the `ManagedCluster` on the hub. Slack becomes fleet-aware:
`@remedik status on prod-eu`, `@remedik clusters`, per-channel default
clusters.

**Trade-off, stated plainly**: the hub holds credentials for all spokes.
Opt-in only; tokens are minimal-RBAC and rotatable; standalone remains the
default topology.

## Cloud packs ("I don't have a pipeline")

```yaml
packs:
  awsNodes:
    enabled: true
    auth: irsa        # irsa | workloadIdentity | secretRef
```

`action: node.replace` then works without external CI: cordon → drain →
terminate the VM via the cloud API (ASG / VMSS / MIG); the node pool brings
up the replacement. Teams with CI keep using `webhook.call` — same strategy
YAML either way.

## Audit sinks & reports

```yaml
audit:
  sinks:
    - type: splunk-hec
      url: https://splunk.example.com:8088
      tokenSecretRef: splunk-hec-token
    - type: loki
      url: http://loki.monitoring.svc:3100
reports:
  weeklyDigest: true    # "what I did" / "what I would have done" (dry-run)
  channel: "#platform-team"
```

Structured events per step (who approved, what ran, what happened), with
retry and local buffering. Supported sink types at GA target: `splunk-hec`,
`loki`, `elasticsearch`, `s3` (JSONL), `webhook`.

## AI diagnosis (BYO-LLM, read-only)

```yaml
ai:
  enabled: true
  provider: openai-compatible    # or: anthropic
  endpoint: http://ollama.ai.svc:11434/v1
  model: qwen2.5:14b
  apiKeySecretRef: remedik-ai-key   # optional for in-cluster servers
  redactSecrets: true
```

`@remedik diagnose deploy/payments` gathers read-only context (describe,
events, recent logs), asks *your* model, and posts probable cause +
suggested strategy to Slack. The AI never executes anything — see
[ADR-0003](adr/0003-deterministic-core-ai-read-only.md).
