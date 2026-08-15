# Advanced setup

> **Status: placeholder.** Each section below is a design commitment from
> the architecture plan and becomes concrete, tested documentation when its
> capability ships. Target values schemas are shown so early adopters can
> see where remedik is going; they are kept in sync with the specs.

## Execution modes & notifications

Per-strategy `execution.mode` (`auto` / `approval` / `manual`) and
`notify.level` (`none` / `onCompletion` / `verbose`) — see
[architecture](architecture.md#execution-modes-per-strategy). *(Ships with
the Slack change.)*

## Install profiles

```yaml
profile: standard   # minimal | standard | full
```

- `minimal` — gateway + engine + CRDs.
- `standard` — + Slack bot, metrics, bundled Grafana dashboard.
- `full` — + pipelines area (cloud packs + runbook runner) and GUI; for
  greenfield clusters, optional kube-prometheus-stack subchart (disabled by
  default).

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
