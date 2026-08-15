## Why

remedik currently has no runtime behavior — the repository is scaffolding.
This change specifies the MVP core: the deterministic loop that turns an
Alertmanager alert into a safe, audited remediation on a single cluster.
Everything later (Slack approvals, escalation, packs, hub/spoke) builds on
these contracts, so they must land first and land well-specified.

## What Changes

- Add an HTTP **alert gateway** that receives Alertmanager webhook posts,
  authenticates them, and normalizes grouped payloads into individual alert
  events.
- Add the **`RemediationStrategy` CRD** (`remedik.dev/v1alpha1`): trigger
  matching, execution mode (`auto` only in this change), guards (`cooldown`,
  `maxPerHour`), ordered steps, failure policy.
- Add the **`Remediation` CRD** as the per-execution record: state machine
  and full audit trail, including simulated (dry-run) executions.
- Add the **execution engine**: matches events to strategies, evaluates
  guards, executes steps sequentially, honors global dry-run, retries with
  backoff, records outcomes.
- Add the first built-in action, **`deployment.restart`**.
- Add working **Helm templates** (CRDs, Deployment, RBAC, gateway Service)
  so `helm install` produces a functioning dry-run-by-default operator.

## Non-goals

- Slack bot, approval/manual execution modes, notifications (next change).
- Escalation (PagerDuty), audit sinks, GUI, AI diagnosis, hub/spoke, cloud
  packs, `ActionPlugin`.
- Any action beyond `deployment.restart`.

## Capabilities

### New Capabilities

- `alert-gateway`
- `remediation-strategies`
- `remediation-execution`
- `action-deployment-restart`

### Modified Capabilities

(none — this is the first change)

## Impact

- New Go packages under `api/` and `internal/` (gateway, engine, actions).
- New CRDs installed by the chart; feature-scoped RBAC for the restart action.
- `QUICKSTART.md` section 2 moves from "target UX" to "current" when this
  ships; `docs/architecture.md` status labels updated.
