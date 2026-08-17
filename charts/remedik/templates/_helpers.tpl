{{/*
Chart name, overridable.
*/}}
{{- define "remedik.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified release name.
*/}}
{{- define "remedik.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else if contains (include "remedik.name" .) .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "remedik.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "remedik.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "remedik.labels" -}}
helm.sh/chart: {{ include "remedik.chart" . }}
{{ include "remedik.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "remedik.selectorLabels" -}}
app.kubernetes.io/name: {{ include "remedik.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "remedik.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "remedik.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Name of the Secret holding the gateway bearer token.

Either the user points at their own Secret with gateway.auth.existingSecret,
or the chart creates one from gateway.auth.token.
*/}}
{{- define "remedik.gatewayTokenSecret" -}}
{{- if .Values.gateway.auth.existingSecret -}}
{{- .Values.gateway.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-gateway-token" (include "remedik.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Name of the Secret holding the dashboard token.

Either the user points at their own Secret with dashboard.auth.existingSecret,
or the chart creates one from dashboard.auth.token.
*/}}
{{- define "remedik.dashboardTokenSecret" -}}
{{- if .Values.dashboard.auth.existingSecret -}}
{{- .Values.dashboard.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-dashboard-token" (include "remedik.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Look up a feature's config by the key action-rbac.yaml uses.

Most keys are actions; blastRadius is a guard. Both are named features that
hold a permission only while they are enabled, so both are looked up the
same way rather than the template growing a special case.
*/}}
{{- define "remedik.featureConfig" -}}
{{- $root := .root -}}
{{- $key := .key -}}
{{- if hasKey $root.Values.actions $key -}}
{{- index $root.Values.actions $key | toYaml -}}
{{- else if hasKey $root.Values.guards $key -}}
{{- index $root.Values.guards $key | toYaml -}}
{{- else -}}
{}
{{- end -}}
{{- end -}}

{{/*
The action names this release enables, as remedik spells them.

Kept next to the RBAC table it mirrors: the chart grants an action's
permissions and registers the action itself from the same decision, so the
two cannot drift apart into an operator that may do something it cannot be
asked to do, or vice versa.
*/}}
{{- define "remedik.actionNames" -}}
deploymentRestart: deployment.restart
workloadRestart: workload.restart
podDelete: pod.delete
jobDelete: job.delete
deploymentRollback: deployment.rollback
deploymentScale: deployment.scale
hpaScale: hpa.scale
nodeCordon: node.cordon
nodeUncordon: node.uncordon
nodeDrain: node.drain
pvcExpand: pvc.expand
webhookCall: webhook.call
jobRun: job.run
scriptRun: script.run
{{- end -}}

{{- define "remedik.enabledActions" -}}
{{- $names := include "remedik.actionNames" . | fromYaml -}}
{{- $enabled := list -}}
{{- range $key, $verb := $names -}}
{{- $config := index $.Values.actions $key | default dict -}}
{{- if $config.enabled -}}
{{- $enabled = append $enabled $verb -}}
{{- end -}}
{{- end -}}
{{- join "," $enabled -}}
{{- end -}}

{{/*
Fail early on a configuration that cannot work, with a message that says
what to do about it.
*/}}
{{- define "remedik.validateValues" -}}
{{- if and (not .Values.gateway.auth.disabled) (not .Values.gateway.auth.token) (not .Values.gateway.auth.existingSecret) -}}
{{- fail "\nremedik: the gateway needs a bearer token so only Alertmanager can submit alerts.\nSet one of:\n  gateway.auth.token=<value>            (the chart creates the Secret)\n  gateway.auth.existingSecret=<name>    (you manage the Secret; key: token)\nTo run without authentication — local development only — set gateway.auth.disabled=true.\n" -}}
{{- end -}}
{{- if and .Values.networkPolicy.enabled (not .Values.networkPolicy.gatewayFrom) -}}
{{- fail "\nremedik: networkPolicy.enabled is set but networkPolicy.gatewayFrom is empty.\nThat policy would stop Alertmanager reaching the gateway, and nothing would say so:\nremediation would simply stop happening.\nName who may reach it, for example:\n  networkPolicy:\n    gatewayFrom:\n      - namespaceSelector:\n          matchLabels:\n            kubernetes.io/metadata.name: monitoring\n" -}}
{{- end -}}
{{- if and .Values.dashboard.enabled (not .Values.dashboard.auth.disabled) (not .Values.dashboard.auth.token) (not .Values.dashboard.auth.existingSecret) -}}
{{- fail "\nremedik: the dashboard shows alert labels, namespaces and workload names, so it needs a token.\nSet one of:\n  dashboard.auth.token=<value>            (the chart creates the Secret)\n  dashboard.auth.existingSecret=<name>    (you manage the Secret; key: token)\nTo serve it without authentication — local development only — set dashboard.auth.disabled=true.\n" -}}
{{- end -}}
{{- end -}}

{{/*
The kill switch's name and whether to create it, read so that a missing `pause`
block means the defaults rather than an error.

This exists because `helm upgrade --reuse-values` does NOT merge the new chart's
defaults: it replays the previous release's values, so every key added since is
absent, and a template that reaches into one fails the upgrade. That happened
here twice — once rendering an empty duration into an alert rule the Prometheus
operator then rejected, once dereferencing this block. `hack/reuse-values.sh`
now renders the chart against the last release's values on every `make verify`.

The kill switch in particular defaults to existing rather than to being absent.
An upgrade that quietly leaves a cluster without the one command that stops
remediation is the wrong side to fail on.
*/}}
{{- define "remedik.pauseConfigMapName" -}}
{{- (.Values.pause).configMapName | default "remedik-pause" -}}
{{- end -}}

{{- define "remedik.pauseCreate" -}}
{{- $pause := .Values.pause | default dict -}}
{{- if hasKey $pause "create" -}}{{ $pause.create }}{{- else -}}true{{- end -}}
{{- end -}}

