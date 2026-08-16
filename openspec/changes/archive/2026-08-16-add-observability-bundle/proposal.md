## Why

remedik is instrumented and unmonitored. Nine metrics are served on the
manager's endpoint, and nothing in the chart tells Prometheus to scrape
them: kube-prometheus-stack discovers `ServiceMonitor` resources, not
Services, so a stock install collects exactly zero `remedik_*` series. The
metrics have been there since the MVP and have never once been graphed.

That is a gap in three directions:

1. **Nobody can see it working.** The dashboard answers "what happened to
   this workload?"; nothing answers "how has remediation behaved this week?"
   A screenshot of remediations by outcome over seven days is the most
   persuasive artefact this project could have, and it cannot be produced.
2. **Nobody is watching the watcher.** remedik holds write access to a
   cluster. If it crash-loops, stops receiving alerts, or starts failing
   every remediation, the only signal is someone noticing that nothing has
   been remediated lately.
3. **Some questions have no metric at all.** Whether the operator is in
   dry-run, how many strategies are enabled, how many remediations are in
   flight — all visible on the dashboard, none of them queryable, so none of
   them alertable or graphable.

## What Changes

- **Four metrics that describe the operator's posture**, not just its
  throughput: `remedik_build_info`, `remedik_dry_run`,
  `remedik_strategies` by enabled state, and `remedik_remediation_records`
  by state. The last two come from a collector that reads the manager's
  cache when Prometheus scrapes, so they are accurate rather than polled.
- **An optional `ServiceMonitor`**, with configurable labels, because
  kube-prometheus-stack selects on them and the default selector is
  `release: <name>`. Off by default, since a cluster may scrape by other
  means.
- **An optional `PrometheusRule`** with six alerts about remedik itself:
  down, ingest failing, alerts arriving but never matching, remediations
  failing, alert deliveries truncated, and unauthenticated attempts.
- **A Grafana dashboard**, versioned in the repository as JSON and
  optionally shipped as a ConfigMap the Grafana sidecar picks up.

## Non-goals

- Shipping recording rules. The queries are cheap and the cardinality is
  low; pre-computing them would be optimising a problem nobody has.
- Requiring the Prometheus Operator. Everything here is opt-in, and the
  metrics endpoint stays a plain scrape target for anyone using something
  else.
- Alerting on the workloads remedik remediates. That is the cluster's own
  monitoring, and it is what produces the alerts remedik consumes.

## Capabilities

### New Capabilities

- `operator-observability`

## Impact

- `internal/metrics`: four metrics and a collector interface that stays free
  of Kubernetes types, fed from the engine.
- `charts/remedik/`: `serviceMonitor.*`, `prometheusRule.*` and
  `grafanaDashboard.*` values, all off by default, plus
  `dashboards/remedik.json`.
- No new RBAC: the collector reads what the operator already watches.
