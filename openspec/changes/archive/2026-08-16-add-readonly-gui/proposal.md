## Why

Today the only way to see what remedik has done is `kubectl get
remediations` and the operator's logs. That is enough for the person who
installed it and nobody else. The two questions the product exists to
answer — "what would this have done?" during the dry-run trial, and "why
did nothing happen?" during an incident — are exactly the questions that
are painful to answer through kubectl, because they need several resources
read together and formatted.

A dry-run trial is also the moment remedik has to earn trust, and a report
someone can open in a browser and show their team is worth more than a
YAML dump that only they can read.

## What Changes

- Add a **read-only web dashboard** served by the operator itself, on its
  own port, with three pages:
  - **Overview** — remediation counts by outcome, the dry-run summary
    ("what I would have done"), and the most recent executions.
  - **Remediation detail** — the triggering alert, the plan, per-step
    outcome, timings and attempts for one execution.
  - **Strategies** — every strategy with its enabled state, guards, steps
    and last run.
- The dashboard is **disabled by default** and, when enabled, serves a
  ClusterIP Service only; the chart ships no Ingress.
- Optional bearer-token authentication, reusing the gateway's pattern.
- No new RBAC: the dashboard reads the same resources the operator already
  reads.

## Non-goals

- **Any mutating action.** No approve, no re-run, no enable/disable, no
  delete. Writes belong to kubectl and, later, the Slack bot, where the
  audit trail records who asked. A dashboard with buttons would need an
  identity model this change is not introducing.
- A JavaScript build toolchain. Pages are rendered server-side and shipped
  inside the binary.
- Multi-cluster views, SSO, and per-user permissions — these belong with
  the hub/spoke change and the open-core discussion.
- Charts and time-series. Prometheus and Grafana already do that better;
  the dashboard links to metrics rather than reimplementing them.

## Capabilities

### New Capabilities

- `readonly-dashboard`

### Modified Capabilities

(none — the dashboard only reads what the existing capabilities produce)

## Impact

- New package `internal/dashboard`, embedded templates and assets, a new
  manager Runnable, and new chart values (`dashboard.*`) plus a Service.
- The operator's image grows by the embedded templates and CSS, on the
  order of tens of kilobytes.
- `docs/architecture.md` moves the GUI row from planned to shipped;
  QUICKSTART gains a section on reading the dry-run report.
