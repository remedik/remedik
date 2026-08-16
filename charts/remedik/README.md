# remedik

![Version: 0.1.0-alpha.1](https://img.shields.io/badge/Version-0.1.0--alpha.1-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0-alpha.1](https://img.shields.io/badge/AppVersion-0.1.0--alpha.1-informational?style=flat-square)

Predictably boring auto-remediation for Kubernetes alerts.

> **Alpha.** The API group `remedik.dev/v1alpha1` may change between
> releases. Every change is specified in `openspec/` before it lands.

## Install

The gateway needs a bearer token so only Alertmanager can submit alerts:

```bash
helm install remedik oci://ghcr.io/remedik/charts/remedik \
  --namespace remedik --create-namespace \
  --set gateway.auth.token="$(openssl rand -hex 24)"
```

**Dry-run is the install default.** remedik matches alerts, evaluates guards
and records what it would have done, changing nothing, until you set
`dryRun=false`.

**Posture is per namespace.** `dryRun` is the default and `namespacePosture`
overrides it, so one install can act where remediation has been earned and
report everywhere else:

```yaml
dryRun: true
namespacePosture:
  staging: live       # act here
  # everything else only reports
```

It works in the other direction too — `dryRun: false` with `prod: dryRun`.
The namespace is the **workload's**, not remedik's, and a target with no
namespace — a node, a webhook — takes `dryRun`. Read the two together: with
an override present, `dryRun: true` alone does not mean nothing acts, which
is why the install notes print the overrides and the dashboard's badge reads
`Mixed`.

To stop everything immediately, scale the deployment to zero or disable the
strategy. Both are instant and neither needs a chart upgrade.

## Upgrading

Custom resource definitions are installed from the chart's `crds/`
directory. Helm installs them on first install and **never upgrades them**,
so a `helm upgrade` alone leaves the old CRD in place and every field added
by the new version is rejected with `unknown field`. Apply them yourself,
before the upgrade:

```console
helm pull oci://ghcr.io/remedik/charts/remedik --version <new> --untar
kubectl apply --server-side --force-conflicts -f remedik/crds/
helm upgrade remedik oci://ghcr.io/remedik/charts/remedik --version <new> -n remedik
```

`--server-side` avoids the annotation size limit these CRDs are large enough
to hit, and `--force-conflicts` takes the field ownership Helm recorded on
first install. Helm never deletes them either, which is deliberate: removing
a CRD deletes every `Remediation` in the cluster with it.

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
| actions.deploymentRestart.enabled | bool | `true` | Enable `deployment.restart`: a rolling restart of a Deployment. Disabling it also removes the RBAC rule that lets remedik patch Deployments. |
| actions.deploymentRollback.enabled | bool | `false` | Enable `deployment.rollback`: puts the previous revision back, the way `kubectl rollout undo` does. The highest-value action here — the usual cause of a 3am crash loop is the deploy ten minutes earlier — and the one most likely to surprise: it refuses a workload Argo CD or Flux manages, because they would revert it within minutes while remedik recorded a success. |
| actions.deploymentScale.enabled | bool | `false` | Enable `deployment.scale`: sets or increases replicas, bounded by a maximum the strategy must state. Refuses a Deployment a HorizontalPodAutoscaler owns, since the autoscaler would revert it. |
| actions.hpaScale.enabled | bool | `false` | Enable `hpa.scale`: raises an autoscaler's `maxReplicas`, the one mechanical answer to `KubeHpaMaxedOut`. It never lowers one. |
| actions.jobDelete.enabled | bool | `false` | Enable `job.delete`: deletes a failed Job, and its pods with it, so the CronJob that owns it creates a clean run. |
| actions.jobRun.enabled | bool | `false` | Enable `job.run`: runs a container image as a Job in remedik's own namespace, with the alert's labels as environment variables. The Job runs under a ServiceAccount your strategy names — never remedik's, which is refused — so its authority is granted deliberately rather than inherited. This is the widest action in the catalogue; enable it when you have decided which ServiceAccount it may use. |
| actions.nodeCordon.enabled | bool | `false` | Enable `node.cordon`: stop new work landing on a node. The safest action here — nothing moves, nothing restarts, one command undoes it — and the right first response to almost every node alert. |
| actions.nodeDrain.enabled | bool | `false` | Enable `node.drain`: cordon a node, then evict its pods through the Eviction API, honouring PodDisruptionBudgets. **This is the widest permission remedik holds** — `list` on pods and `create` on `pods/eviction` across every namespace — and there is no narrower way to drain a node. Enable it last, and leave it in dry-run longer than the rest: the dry-run report names every pod that would move. |
| actions.nodeUncordon.enabled | bool | `false` | Enable `node.uncordon`: the undo. Separate from the cordon so a strategy can be granted one without the other. |
| actions.podDelete.enabled | bool | `false` | Enable `pod.delete`: evicts one pod through the Eviction API, so a PodDisruptionBudget can refuse it. It cannot delete pods outright — the permission granted is `create` on `pods/eviction`, not `delete` on `pods`. Refuses a pod with no controller owner, since nothing would recreate it. |
| actions.pvcExpand.enabled | bool | `false` | Enable `pvc.expand`: grow a PersistentVolumeClaim, but only where the StorageClass allows expansion. Where it does not, the API accepts the change and nothing happens, so the action checks first and refuses. Expansion is one-way: Kubernetes cannot shrink a volume back. |
| actions.scriptRun.enabled | bool | `false` | Enable `script.run`: `job.run` with the script taken from a ConfigMap in remedik's namespace, so a runbook can be edited without rebuilding an image. The ConfigMap is read from remedik's namespace only: reading one from anywhere else would let anyone with write access to any namespace get code executed by the operator. |
| actions.webhookCall.enabled | bool | `false` | Enable `webhook.call`: POSTs the alert, the strategy and the plan to a URL you configure, optionally with a token from a Secret in remedik's namespace. The cheapest way to reach a pipeline remedik will never implement, and it moves the blast radius outside the cluster. |
| actions.workloadRestart.enabled | bool | `false` | Enable `workload.restart`: the same rolling restart for Deployments, StatefulSets and DaemonSets. Off by default because it grants patch on all three; if you only ever restart Deployments, leave this off and use `deployment.restart`. |
| affinity | object | `{}` | Affinity for the operator pod |
| clusterName | string | `""` | A name for this cluster, shown in the dashboard's header and browser tab. Purely a label: remedik watches the cluster it runs in, so this is what tells three port-forwarded dashboards apart, not a filter. |
| dashboard.auth.disabled | bool | `false` | Serve the dashboard without authentication. Anything that can reach the port could then read every alert label, namespace and workload name remedik has recorded. |
| dashboard.auth.existingSecret | string | `""` | Name of a Secret you manage that holds the dashboard token |
| dashboard.auth.secretKey | string | `"token"` | Key inside the token Secret |
| dashboard.auth.token | string | `""` | Token required to read the dashboard. The chart creates a Secret holding it. Present it as a bearer token, or as the password in the browser's own prompt (the username is ignored). Prefer `existingSecret` in production so the value is not in your values file. |
| dashboard.enabled | bool | `false` | Serve the read-only web dashboard. Off by default: its pages disclose alert labels, namespaces and workload names, and deciding who may see those is the cluster owner's call, not the chart's. Enabling it grants remedik no additional permission — the dashboard reads what the operator already reads. |
| dashboard.port | int | `8082` | Port the dashboard listens on |
| dryRun | bool | `true` | Default execution posture. The install default is dry-run: remedik matches alerts, evaluates guards and records Simulated remediations, changing nothing. Turn it off once the reports look right.  Read this together with `namespacePosture` below: a namespace named there overrides this, so `dryRun: true` alone does not mean nothing acts. |
| fullnameOverride | string | `""` | Override the fully qualified release name |
| gateway.auth.disabled | bool | `false` | Disable authentication entirely. Local development only: anything that can reach the service could then ask remedik to act. |
| gateway.auth.existingSecret | string | `""` | Name of a Secret you manage that holds the bearer token |
| gateway.auth.secretKey | string | `"token"` | Key inside the token Secret |
| gateway.auth.token | string | `""` | Bearer token Alertmanager must present. The chart creates a Secret holding it. Prefer `existingSecret` in production so the value is not in your values file. |
| gateway.path | string | `"/webhooks/alertmanager"` | Path the Alertmanager webhook is served on |
| gateway.port | int | `8090` | Port the Alertmanager webhook receiver listens on |
| grafanaDashboard.annotations | object | `{}` | Extra annotations, such as the sidecar's folder annotation |
| grafanaDashboard.enabled | bool | `false` | Ship the remedik Grafana dashboard as a ConfigMap for the Grafana sidecar to load. The JSON is versioned in the chart, so the dashboard is the same in every cluster rather than living in one person's browser. |
| grafanaDashboard.label | string | `"grafana_dashboard"` | Label the Grafana sidecar watches for |
| grafanaDashboard.labelValue | string | `"1"` | Value of that label |
| grafanaDashboard.namespace | string | `""` | Namespace for the ConfigMap; defaults to the release namespace. The Grafana sidecar usually only watches its own namespace. |
| guards.blastRadius.enabled | bool | `false` | Allow strategies to use the `blastRadius` guard, which refuses to remediate a workload that is already too degraded. Enabling it grants read-only access to workloads, pods and replicasets so the guard can see how much is available.  A strategy that sets `blastRadius` in a cluster where this is off will be **refused, not allowed**: a guard that cannot evaluate its own condition must not permit the execution. The refusal names the missing permission on the strategy's events. |
| history.keepPerStrategy | int | `200` | Terminal Remediation records retained per strategy |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"ghcr.io/remedik/remedik"` | Container image repository |
| image.tag | string | `""` | Image tag; defaults to the chart appVersion |
| logLevel | string | `"info"` | Log level: debug, info, warn or error |
| metrics.port | int | `8080` | Port the Prometheus metrics endpoint listens on |
| nameOverride | string | `""` | Override the chart name |
| namespacePosture | object | `{}` | Per-namespace overrides of `dryRun`, keyed by the namespace of the workload being remediated — not by remedik's own namespace. Each value is `live` (remedik acts) or `dryRun` (remedik only reports).  This is how a cluster adopts remediation: live where it has been earned, reporting everywhere else, in one install.    dryRun: true                 # report by default   namespacePosture:     staging: live              # ...but act in staging    dryRun: false                # act by default   namespacePosture:     prod: dryRun               # ...but only report in prod  A remediation whose target has no namespace — a node, a webhook — uses `dryRun` above. The posture is resolved once, when the record is created, and written onto it, so every Remediation says which posture it ran under and an in-flight execution keeps the one it started with.  To stop everything immediately, scale the deployment to zero or disable the strategy; both are instant and neither needs a chart upgrade. |
| networkPolicy.dashboardFrom | list | `[]` | Who may reach the read-only dashboard. Empty allows nothing, which is the right default: reach it with `kubectl port-forward`, which the kubelet proxies and a NetworkPolicy does not govern. |
| networkPolicy.enabled | bool | `false` | Restrict who may reach remedik's ports. Off by default because a policy naming the wrong peers stops Alertmanager silently, and silence is this project's worst failure mode. Ingress only: remedik's one outbound call is to the API server, whose address is specific to your cluster. |
| networkPolicy.gatewayFrom | list | `[]` | Who may reach the gateway — the port that makes the cluster change itself. Required when the policy is enabled. A list of NetworkPolicy peers, for example:   - namespaceSelector:       matchLabels:         kubernetes.io/metadata.name: monitoring |
| networkPolicy.metricsFrom | list | `[]` | Who may scrape metrics. Defaults to whoever may reach the gateway, which is right when Alertmanager and Prometheus are the same install. |
| nodeSelector | object | `{}` | Node selector for the operator pod |
| podAnnotations | object | `{}` | Extra annotations for the operator pod |
| priorityClassName | string | `""` | PriorityClass for the operator pod. A single-replica operator evicted under node pressure stops remediating without anyone being told, so on a busy cluster this is worth setting to something like `system-cluster-critical`. |
| probes.port | int | `8081` | Port the health and readiness probes listen on |
| prometheusRule.additionalLabels | object | `{}` | Labels the PrometheusRule needs to be selected, as for the ServiceMonitor |
| prometheusRule.enabled | bool | `false` | Create a PrometheusRule alerting on remedik itself: down, ingest failing, nothing matching, remediations failing, deliveries truncated, unauthenticated attempts. Something that holds write access to a cluster should be watched by the same monitoring it consumes. |
| prometheusRule.namespace | string | `""` | Namespace for the PrometheusRule; defaults to the release namespace |
| prometheusRule.severity.critical | string | `"critical"` | Severity label for the rule that fires when remedik is not scraped |
| prometheusRule.severity.warning | string | `"warning"` | Severity label for the rest |
| resources.limits.memory | string | `"128Mi"` |  |
| resources.requests.cpu | string | `"50m"` |  |
| resources.requests.memory | string | `"64Mi"` |  |
| serviceAccount.annotations | object | `{}` | Annotations for the ServiceAccount (for example, cloud identity) |
| serviceAccount.create | bool | `true` | Create a ServiceAccount for the operator |
| serviceAccount.name | string | `""` | Name of the ServiceAccount; generated when empty |
| serviceMonitor.additionalLabels | object | `{}` | Labels the ServiceMonitor needs to be selected. kube-prometheus-stack selects on `release: <its release name>` by default, so this is usually `{release: monitoring}`. A ServiceMonitor without it is created, ignored, and hard to notice. |
| serviceMonitor.enabled | bool | `false` | Create a ServiceMonitor so the Prometheus Operator scrapes remedik. Off by default because not every cluster uses the operator — but without it, or an equivalent scrape config, remedik is instrumented and unmonitored: a Service alone is not discovered. |
| serviceMonitor.interval | string | `"30s"` | How often Prometheus scrapes remedik |
| serviceMonitor.metricRelabelings | list | `[]` | Relabelings applied to the scraped metrics |
| serviceMonitor.namespace | string | `""` | Namespace for the ServiceMonitor; defaults to the release namespace |
| serviceMonitor.relabelings | list | `[]` | Relabelings applied before the scrape |
| serviceMonitor.scrapeTimeout | string | `"10s"` | How long a scrape may take. The posture metrics read the operator's cache, so a slow scrape means the cache is gone. |
| tolerations | list | `[]` | Tolerations for the operator pod |

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| raTyx |  |  |
