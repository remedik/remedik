## Context

The metrics have existed since the MVP. Nothing has ever scraped them,
because kube-prometheus-stack discovers `ServiceMonitor` resources and the
chart only ever created a Service. The gap was invisible precisely because
metrics that are never scraped produce no error anywhere.

## Decisions

1. **A Prometheus collector, not a gauge on a timer.** The strategy and
   record counts are already in the manager's cache; reading them when
   Prometheus asks is cheaper than keeping a second copy correct, and it
   cannot go stale. The collector is registered only when a snapshot
   function is supplied, so the metrics package stays usable without a
   cluster.

2. **The metrics package still knows nothing about Kubernetes.** The
   snapshot arrives as a plain struct through a function type. The
   dependency in this project runs engine → metrics: the engine says what it
   can report, and the metrics package decides how to publish it. Inverting
   that for convenience would have created an import cycle, and the cycle is
   the design telling us which way round it goes.

3. **A failed snapshot reports nothing, not zero.** Zero enabled strategies
   means remediation cannot happen — a real and alarming value. Emitting it
   because a read failed would turn a monitoring failure into a false
   incident, which is the specific way monitoring loses people's trust.

4. **Everything in the chart is off by default.** A ServiceMonitor in a
   cluster without the Prometheus Operator is a manifest that fails to
   apply; a PrometheusRule with the wrong labels is silently ignored. Both
   are opt-in, and both take the labels the cluster's own operator selects
   on, because those differ per install and the default is nobody's.

5. **The dashboard JSON is versioned in the chart, and colours are pinned by
   series name.** Grafana assigns palette colours by series order, so a
   filter that removes a series repaints the survivors — "Failed" becomes
   the colour "Succeeded" had. Every outcome, guard and state is pinned with
   a `byName` override, so colour follows the entity rather than its rank.

6. **Grafana's named colours, not hex.** Named colours are theme-aware, so
   the dashboard reads correctly in both Grafana themes. Checked with a
   contrast and colour-vision validator: every pair on every panel separates
   for protanopia, deuteranopia and tritanopia, and clears 3:1 against the
   surface. The palette sits outside the validator's preferred lightness
   band, which is a property of Grafana's own theme; pinning hex values to
   fix that would break light mode, and the checks that decide whether two
   series can be told apart all pass.

## Risks / Trade-offs

- **Six alert rules are six chances to be wrong about somebody's cluster.**
  Each has a `for:` long enough to survive a rollout, and the thresholds are
  ratios rather than absolute counts, so a small cluster and a large one
  behave the same. They are still opt-in.
- **The collector runs on the scrape path.** A slow cache makes scrapes slow.
  Bounded at five seconds, after which the series are absent — which is what
  a scrape timeout would have produced anyway, but logged.

## Open Questions

None blocking. Recording rules are deliberately absent: the queries are
cheap and the cardinality is low, so pre-computing them would optimise a
problem nobody has yet.
