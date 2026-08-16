# operator-observability Specification

## Purpose

Make remedik monitorable by the same Prometheus that feeds it: metrics that
describe its posture as well as its throughput, a way for Prometheus to
discover them, alerts about remedik itself, and a dashboard that ships with
the chart rather than living in one person's browser.

## Requirements

### Requirement: Posture metrics

The operator SHALL expose, alongside its counters, metrics describing what
it currently is: the running version, whether it is in dry-run, how many
strategies are enabled and disabled, and how many Remediation records exist
by state.

The counts that depend on cluster state SHALL be read when Prometheus
scrapes, from the manager's cache, and SHALL NOT contact the API server: a
scrape must not turn Prometheus's polling interval into load on the control
plane. A read that fails SHALL report no series rather than zero, because
zero strategies is a meaningful value and reporting it because a read failed
would be a graph that lies quietly.

#### Scenario: A dashboard can tell dry-run from a quiet week

- **WHEN** the operator is in dry-run
- **THEN** `remedik_dry_run` is 1, so a flat remediation rate is explainable rather than alarming

#### Scenario: A failed read is absent, not zero

- **WHEN** the cache cannot be read during a scrape
- **THEN** the strategy and record gauges are absent from the response, and the failure is logged

### Requirement: Prometheus discovery

The chart SHALL be able to create a `ServiceMonitor` for the metrics
endpoint, with configurable labels, and SHALL NOT create one by default.

Labels are configurable because they are load-bearing: the Prometheus
Operator selects ServiceMonitors by label, and one created without the
selector's label is created, ignored, and difficult to notice.

#### Scenario: Not created unless asked for

- **WHEN** the chart is installed with default values
- **THEN** no ServiceMonitor exists, and the metrics endpoint is still a plain scrape target for anything else

#### Scenario: Selectable by the Prometheus Operator

- **WHEN** `serviceMonitor.additionalLabels` names the operator's selector label
- **THEN** the rendered ServiceMonitor carries it

### Requirement: Alerts about remedik itself

The chart SHALL be able to create a `PrometheusRule` alerting on remedik's
own health: not being scraped, failing to ingest deliveries, receiving
alerts that never match a strategy, failing most remediations, receiving
truncated deliveries, and repeated unauthenticated attempts.

The rules SHALL be about the operator, never about the workloads it
remediates: those already have alerts, and those alerts are remedik's input.

#### Scenario: A silent operator is noticed

- **WHEN** remedik stops being scraped for ten minutes
- **THEN** an alert fires saying nothing is being remediated or recorded

### Requirement: A dashboard that ships with the chart

The repository SHALL contain the Grafana dashboard as versioned JSON, and
the chart SHALL be able to ship it as a ConfigMap for the Grafana sidecar,
with a configurable label.

#### Scenario: The same dashboard in every cluster

- **WHEN** the dashboard is enabled
- **THEN** the ConfigMap carries the JSON from the chart, so no cluster's dashboard is somebody's unsaved browser tab
