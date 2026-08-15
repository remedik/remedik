## Context

Developer tooling requested by the project owner; no spec-level behavior
changes (`skip_specs: true`). Key decisions and their reasoning below.

## Decisions

1. **golangci-lint v2.12.2, pinned** — installed via the official install
   script into `hack/bin/` (the project-recommended method; `go install` is
   discouraged by upstream). Config uses the v2 format with the `standard`
   default set plus `misspell`, `gocritic`, `revive`.
2. **yamlfmt instead of yamlfix** — the requested "yamlfix" is a Python
   tool, which would add a pip dependency to a Go repo; `google/yamlfmt`
   does the same job, installs with `go install`, and is configured to
   retain single line breaks. The target keeps the requested ergonomics:
   `make yaml-fix`.
3. **yamllint stays external** — it is Python-based with no Go equivalent
   of comparable coverage; `make yaml-lint` checks for it and prints the
   apt/pip install hint instead of mutating the developer's Python
   environment.
4. **`make verify` == CI, deterministically** — verify runs the entire
   gate including helm lint; helm is a hard prerequisite (developers need
   it for `dev-up` anyway). No silently-skipped checks.
5. **Dev cluster via kind + kube-prometheus-stack** — one command gives the
   Alertmanager/Grafana/Prometheus UIs the project needs to demo alerts
   end-to-end. Chart version intentionally tracks latest during pre-alpha
   (`KPS_CHART_VERSION` variable exists for pinning once the e2e suite
   depends on it). Grafana dev credentials: admin / remedik-dev.
6. **helm-docs as the chart-docs source of truth** — `values.yaml` carries
   `# --` annotations; CI regenerates and fails on drift, so the chart
   README can never go stale.

## Risks / Trade-offs

- Sandbox/dev environments without proxy.golang.org can't run the
  `go install` tool rules; tools can equally be dropped into `hack/bin/`
  from GitHub release binaries (same pinned versions) — documented here for
  future automation environments.
- kube-prometheus-stack unpinned during pre-alpha trades reproducibility
  for freshness; acceptable until e2e tests depend on the dev cluster.

## Open Questions

None.
