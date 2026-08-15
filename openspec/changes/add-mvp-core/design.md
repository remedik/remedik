## Context

First implementation change; the repo currently ships a probes-only skeleton
binary. The behavior contracts live in this change's specs; this document
fixes the technical approach.

## Goals / Non-Goals

- **Goals**: the smallest end-to-end deterministic loop; correct-by-construction
  audit; `helm install` to a working dry-run on kind in under 10 minutes.
- **Non-goals**: approval/manual modes, notifications, any second action —
  see the proposal's Non-goals.

## Decisions

1. **Runtime shape** — single binary on controller-runtime. The gateway is an
   `http.Server` registered as a manager `Runnable` (default `:8090`), so
   informer caches, metrics and graceful shutdown come from one lifecycle.
   Single replica, no leader election in alpha (documented limitation).
2. **API group `remedik.dev`** — matches the chosen project domain.
   The domain was free at decision time and MUST be registered before any
   public artifact references it. `v1alpha1`; no conversion webhooks in alpha.
3. **CRD-as-store, no database** — `Remediation` CRs are the audit record.
   Pruning (keep most recent 200 terminal records per strategy, configurable)
   ships in this change so storage is bounded from day one.
4. **State machine via status + requeue** — standard controller pattern:
   attempt counter and step cursor live in `Remediation.status`; an
   `Interrupted` sweep at startup implements the crash-safety requirement.
   We deliberately mark interrupted executions failed instead of resuming —
   resuming mutating steps safely requires per-action idempotency proofs we
   don't have yet.
5. **Action interface** — internal registry keyed by `verb.noun` names:
   `Validate(ctx, step) error`, `Plan(ctx, step) (Plan, error)`,
   `Execute(ctx, step) (Result, error)`. Dry-run calls `Plan` only. This is
   the seam where `job`, `script`, and `ActionPlugin` attach later.
6. **Matching** — equality matchers only in v1alpha1. Regex is deferred on
   purpose: predictability first, and most-specific-wins is easy to reason
   about with equality semantics.
7. **Helm layout** — CRDs under `crds/` (installed before templates),
   Deployment + SA + Role/RoleBinding assembled from enabled actions,
   gateway Service, NOTES.txt printing the Alertmanager receiver snippet.
   `helm lint` runs in CI.
8. **Testing** — table-driven unit tests for matching/guards/gateway; envtest
   for the controller; one kind e2e (`make e2e`): install chart → POST
   webhook → assert a `Simulated` Remediation appears.

## Risks / Trade-offs

- **Alert storms** → bounded by `maxPerHour` guard + pruning; gateway stays
  200-fast and drops unmatched events after counting them.
- **Single replica** → acceptable pre-beta; a crash loses in-flight work but
  the `Interrupted` sweep makes that visible, never silent.

## Open Questions

None blocking. Regex matchers, multi-target fan-out, and resolved-alert
handling (auto-close) are deferred to future changes.
