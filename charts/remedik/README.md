# remedik

![Version: 0.0.1-dev](https://img.shields.io/badge/Version-0.0.1--dev-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.1-dev](https://img.shields.io/badge/AppVersion-0.0.1--dev-informational?style=flat-square)

> **Status: skeleton.** Templates land with the `add-mvp-core` OpenSpec
> change; until then this chart is not installable. `values.yaml` is the
> design contract for the v0.1.0 values schema; keys marked `(v0.2.0)`
> belong to later changes.

Predictably boring auto-remediation for Kubernetes alerts (pre-alpha skeleton).

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| ai.enabled | bool | `false` | Read-only BYO-LLM diagnosis, disabled by default — planned for v0.2.0 |
| audit.sinks | list | `[]` | Structured audit export sinks (splunk-hec, loki, elasticsearch, s3, webhook) — planned for v0.2.0 |
| dryRun | bool | `true` | Global execution posture. Install-time default is dry-run: remedik matches, evaluates guards and records Simulated remediations — it acts on nothing until you flip this. |
| escalation.pagerduty.enabled | bool | `false` | Enable PagerDuty escalation — planned for v0.2.0 |
| escalation.pagerduty.routingKeySecretRef | string | `""` | Secret with the PagerDuty Events API routing key — planned for v0.2.0 |
| gateway.auth.bearerTokenSecretRef | string | `""` | Name of the Secret holding the bearer token Alertmanager must send |
| gateway.port | int | `8090` | Port the Alertmanager webhook receiver listens on |
| history.keepPerStrategy | int | `200` | Terminal Remediation records retained per strategy |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"ghcr.io/ratyx/remedik"` | Container image repository |
| image.tag | string | `""` | Image tag; defaults to the chart appVersion |
| packs | object | `{}` | Cloud packs, e.g. `awsNodes: {enabled: true, auth: irsa}` — planned for v0.2.0 |
| profile | string | `"standard"` | Install profile: minimal / standard / full |
| resources.limits.memory | string | `"128Mi"` |  |
| resources.requests.cpu | string | `"50m"` |  |
| resources.requests.memory | string | `"64Mi"` |  |
| slack.appTokenSecretRef | string | `""` | Secret with the Slack app-level token — planned for v0.2.0 |
| slack.botTokenSecretRef | string | `""` | Secret with the Slack bot token — planned for v0.2.0 |
| slack.enabled | bool | `false` | Enable the Socket Mode Slack bot — planned for v0.2.0 |

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| raTyx |  |  |
