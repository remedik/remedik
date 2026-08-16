## 1. Metrics

- [x] 1.1 `remedik_build_info` and `remedik_dry_run`
- [x] 1.2 A collector for `remedik_strategies` and `remedik_remediation_records`, read from the cache on scrape
- [x] 1.3 A failed snapshot reports nothing rather than zero

## 2. Chart

- [x] 2.1 Optional `ServiceMonitor` with configurable labels, interval and relabelings
- [x] 2.2 Optional `PrometheusRule` with six alerts about remedik itself
- [x] 2.3 Optional Grafana dashboard ConfigMap, JSON versioned in the chart

## 3. Proof and docs

- [x] 3.1 Tests: posture metrics, collector output, failed snapshot
- [x] 3.2 `make helm-lint` renders all three, on and off
- [x] 3.3 `make dev-deploy` enables the bundle, so the dev cluster actually graphs it
- [x] 3.4 Docs: an observability section, CHANGELOG, chart README regenerated
