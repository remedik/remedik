## 1. Rendering

- [ ] 1.1 `internal/dashboard`: handler built from a `client.Reader`, templates embedded with `go:embed`, GET/HEAD allowlist, optional bearer token
- [ ] 1.2 View models: map Remediation and RemediationStrategy resources into what each page shows, so templates contain no logic
- [ ] 1.3 Styling: one embedded stylesheet, no external requests, light and dark via `prefers-color-scheme`

## 2. Pages

- [ ] 2.1 Overview: counts by outcome, in-flight count, dry-run summary per strategy, 50 most recent executions, empty-state text
- [ ] 2.2 Remediation detail: alert, plan, per-step phase/message/timings, attempts, terminal reason
- [ ] 2.3 Strategies: enabled state, matchers, guards, steps, last run

## 3. Wiring and packaging

- [ ] 3.1 Register as a manager Runnable on its own port, behind `--dashboard-bind-address`; off unless enabled
- [ ] 3.2 Chart: `dashboard.*` values, ClusterIP Service, container port, token wiring; no Ingress
- [ ] 3.3 Prove the rendered RBAC is byte-identical with the dashboard enabled and disabled

## 4. Tests and docs

- [ ] 4.1 Handler tests: every page renders from fixtures, mutating methods answer 405, 401 without a token, empty state renders
- [ ] 4.2 A test asserting no template references an external origin
- [ ] 4.3 e2e: enable the dashboard, fetch every page, assert the simulated remediation appears in the dry-run summary
- [ ] 4.4 Docs: architecture row planned → shipped, QUICKSTART section on reading the dry-run report, chart README regenerated
