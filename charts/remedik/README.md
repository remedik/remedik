# remedik

![Version: 0.1.0-alpha.1](https://img.shields.io/badge/Version-0.1.0--alpha.1-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0-alpha.1](https://img.shields.io/badge/AppVersion-0.1.0--alpha.1-informational?style=flat-square)

Predictably boring auto-remediation for Kubernetes alerts.

> **Alpha.** The API group `remedik.dev/v1alpha1` may change between
> releases. Every change is specified in `openspec/` before it lands.

## Install

The gateway needs a bearer token so only Alertmanager can submit alerts:

```bash
helm install remedik oci://ghcr.io/ratyx/charts/remedik \
  --namespace remedik --create-namespace \
  --set gateway.auth.token="$(openssl rand -hex 24)"
```

**Dry-run is the install default.** remedik matches alerts, evaluates guards
and records what it would have done, changing nothing, until you set
`dryRun=false`.

Custom resource definitions are installed from the chart's `crds/`
directory. Helm installs them on first install but never upgrades or
deletes them: apply CRD changes yourself when upgrading across versions.

## The dashboard

Off by default. Its pages show alert labels, namespaces and workload names,
so who may see them is your decision — which is also why the chart creates a
ClusterIP Service and never an Ingress.

```bash
helm upgrade remedik ... \
  --set dashboard.enabled=true \
  --set dashboard.auth.token="$(openssl rand -hex 24)"

kubectl -n remedik port-forward svc/remedik-dashboard 8082:8082
```

Open <http://127.0.0.1:8082/>, leave the username empty and paste the token
as the password. Enabling the dashboard adds no RBAC rule: it reads exactly
what the operator already reads.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| actions.deploymentRestart.enabled | bool | `true` | Enable the `deployment.restart` action. Disabling it also removes the RBAC rule that lets remedik patch Deployments. |
| affinity | object | `{}` | Affinity for the operator pod |
| ai.enabled | bool | `false` | Read-only bring-your-own-LLM diagnosis — planned for v0.2.0 |
| audit.sinks | list | `[]` | Structured audit export (Splunk HEC, Loki, Elasticsearch, S3) — planned for v0.2.0 |
| dashboard.auth.disabled | bool | `false` | Serve the dashboard without authentication. Anything that can reach the port could then read every alert label, namespace and workload name remedik has recorded. |
| dashboard.auth.existingSecret | string | `""` | Name of a Secret you manage that holds the dashboard token |
| dashboard.auth.secretKey | string | `"token"` | Key inside the token Secret |
| dashboard.auth.token | string | `""` | Token required to read the dashboard. The chart creates a Secret holding it. Present it as a bearer token, or as the password in the browser's own prompt (the username is ignored). Prefer `existingSecret` in production so the value is not in your values file. |
| dashboard.enabled | bool | `false` | Serve the read-only web dashboard. Off by default: its pages disclose alert labels, namespaces and workload names, and deciding who may see those is the cluster owner's call, not the chart's. Enabling it grants remedik no additional permission — the dashboard reads what the operator already reads. |
| dashboard.port | int | `8082` | Port the dashboard listens on |
| dryRun | bool | `true` | Global execution posture. The install default is dry-run: remedik matches alerts, evaluates guards and records Simulated remediations, changing nothing. Turn it off once the reports look right. |
| escalation.pagerduty.enabled | bool | `false` | PagerDuty escalation — planned for v0.2.0 |
| fullnameOverride | string | `""` | Override the fully qualified release name |
| gateway.auth.disabled | bool | `false` | Disable authentication entirely. Local development only: anything that can reach the service could then ask remedik to act. |
| gateway.auth.existingSecret | string | `""` | Name of a Secret you manage that holds the bearer token |
| gateway.auth.secretKey | string | `"token"` | Key inside the token Secret |
| gateway.auth.token | string | `""` | Bearer token Alertmanager must present. The chart creates a Secret holding it. Prefer `existingSecret` in production so the value is not in your values file. |
| gateway.path | string | `"/webhooks/alertmanager"` | Path the Alertmanager webhook is served on |
| gateway.port | int | `8090` | Port the Alertmanager webhook receiver listens on |
| history.keepPerStrategy | int | `200` | Terminal Remediation records retained per strategy |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"ghcr.io/ratyx/remedik"` | Container image repository |
| image.tag | string | `""` | Image tag; defaults to the chart appVersion |
| logLevel | string | `"info"` | Log level: debug, info, warn or error |
| metrics.port | int | `8080` | Port the Prometheus metrics endpoint listens on |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{}` | Node selector for the operator pod |
| packs | object | `{}` | Cloud packs such as `awsNodes` for node replacement — planned for v0.2.0 |
| podAnnotations | object | `{}` | Extra annotations for the operator pod |
| probes.port | int | `8081` | Port the health and readiness probes listen on |
| resources.limits.memory | string | `"128Mi"` |  |
| resources.requests.cpu | string | `"50m"` |  |
| resources.requests.memory | string | `"64Mi"` |  |
| serviceAccount.annotations | object | `{}` | Annotations for the ServiceAccount (for example, cloud identity) |
| serviceAccount.create | bool | `true` | Create a ServiceAccount for the operator |
| serviceAccount.name | string | `""` | Name of the ServiceAccount; generated when empty |
| slack.enabled | bool | `false` | Socket Mode Slack bot with approval buttons — planned for v0.2.0 |
| tolerations | list | `[]` | Tolerations for the operator pod |

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| raTyx |  |  |
