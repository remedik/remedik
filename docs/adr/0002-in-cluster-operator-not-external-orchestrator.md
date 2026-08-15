# ADR-0002: In-cluster operator, not an external orchestrator

- Status: accepted
- Date: 2026-08-15

## Context

The pattern remedik packages (alert → gates → strategy → retry → escalate)
is proven in internal platforms, which typically run it on an external
orchestrator (Rundeck/Jenkins-style) that launches a container per alert,
with a central cluster registry and a secrets vault holding per-cluster
credentials. That shape carries real operational weight: an extra server to
run, credentials for every cluster concentrated outside them, and a
formatter/forwarder in front of Alertmanager.

## Decision

remedik runs *inside* the cluster it remediates, as a single-binary operator:

- Alertmanager posts webhooks directly to remedik's gateway.
- Strategies and executions are CRDs; state and audit live in the API
  server — no external database.
- The operator acts through its own ServiceAccount with feature-scoped RBAC;
  in the default topology, no cluster credentials ever leave the cluster.
- Multi-cluster (hub/spoke) is a later, explicit opt-in mode with its
  trade-offs documented — not the default.

## Consequences

- One `helm install`, no external infrastructure to operate.
- Per-cluster blast radius by construction in the default topology.
- CRD-as-store scale limits are accepted for v0 (bounded by guards and
  history pruning); revisit with an ADR if execution volume demands it.
