# Working on remedik

Orientation for an AI assistant picking this project up. Humans should read
[README.md](README.md) and [CONTRIBUTING.md](CONTRIBUTING.md) instead; this
file is the short version of what to know before changing anything.

## What this is

A Kubernetes operator that turns Alertmanager alerts into safe, audited
remediation. Alert arrives at the gateway, a strategy matches it, guards
decide, the engine executes, and the outcome is a `Remediation` resource.

## Where the project's memory lives

This repository explains itself. Read these before proposing changes:

- **`openspec/specs/`** — the current behaviour contract, eleven
  capabilities. This is authoritative; code that disagrees with it is a bug
  in one of them. `make specs` checks the workflow was followed.
- **`openspec/changes/archive/`** — what was proposed and why, including the
  reasoning that did not make it into the code.
- **`docs/adr/`** — structural decisions and the arguments behind them.
- **`git log`** — commit messages here explain *why*, not what. They are
  worth reading.
- **`CHANGELOG.md`** — what shipped.

## Invariants — do not break these without an ADR

1. **AI never executes.** LLM-backed features read and explain. The
   execution path is deterministic: declared strategies, guards, and (from
   v0.2.0) human approval. See ADR-0003.
2. **Dry-run is structural, not a flag.** Actions implement `Resolve`,
   `Plan` and `Execute` separately; dry-run calls `Plan`, so the mutating
   path is never reached. Never add a `if dryRun { return }` inside an
   action — that turns a guarantee into a convention.
3. **`Running` means interrupted.** An attempt runs to completion inside a
   single reconcile, so a `Remediation` found in `Running` can only mean the
   process died. It is failed as `Interrupted`, never resumed. Waiting for a
   retry is `Pending`. Changing this breaks crash safety.
4. **RBAC follows features.** The chart grants a permission only because a
   named, enabled feature needs it. If you add a permission, add the feature
   that justifies it, or remove the permission.
5. **Guards must survive a restart.** In-memory guard state is rebuilt from
   existing `Remediation` resources at startup, synchronously, before the
   gateway accepts anything.
6. **The gateway answers 200 to anything it understood**, including "no
   strategy matched". Alertmanager retries non-2xx, so a normal outcome must
   not look like a failure.
7. **An action's authority is named, never inherited.** A remediation Job
   runs as the ServiceAccount its step names — never remedik's, which is
   refused — and Secrets and ConfigMaps are read from remedik's own
   namespace only. A label on an alert must never decide which credential is
   used, or whose code runs.
8. **The dashboard never writes.** It is built from a `client.Reader` and
   allowlists GET and HEAD before routing. Both layers are deliberate: one
   makes a write impossible to call, the other makes it impossible to
   reach. An approve button needs the identity model the Slack change
   introduces, so that the audit trail records *who* asked.
9. **English everywhere** in the repository: code, comments, docs, commits.

## Workflow

Spec-first, and the gate is real:

1. `openspec new change "<kebab-name>"`, then write proposal → specs →
   design → tasks. `openspec validate <name> --strict` must pass.
2. **Get the proposal approved before writing code.**
3. Implement, then `openspec archive <name>` so `openspec/specs/` stays the
   source of truth.

Definition of done: `make verify` passes, docs updated, `CHANGELOG.md`
updated. For anything touching cluster behaviour, `make e2e` too.

## Commands

```bash
make verify        # gofmt, vet, golangci-lint, yamllint, helm lint, specs, race tests
make specs         # the spec-first workflow was actually followed
make e2e           # throwaway kind cluster, the whole loop, then cleanup
make generate manifests   # after changing api/ — CI fails on stale output
make versions      # pinned versions vs. latest upstream
make help          # everything else
```

## Layout

```
api/v1alpha1/        CRDs: RemediationStrategy (cluster) and Remediation (namespaced)
internal/alert/      Alertmanager payload → normalized events   (stdlib only)
internal/gateway/    HTTP receiver, bearer auth                 (stdlib only)
internal/matching/   Which strategy handles an alert            (stdlib only)
internal/guards/     Cooldown and rate limiting                 (stdlib only)
internal/action/     The Resolve/Plan/Execute contract + registry
internal/engine/     Sink (alert → record) and the reconciler
internal/metrics/    Prometheus adapters behind the Recorder interfaces
internal/dashboard/  Read-only web UI; templates and CSS embedded in the binary
internal/action/external/  webhook.call, job.run, script.run — the widest trust surface
charts/remedik/      Helm chart; RBAC assembled from enabled actions
hack/e2e.sh          The end-to-end test
```

The packages marked "stdlib only" are deliberate: the decisions that matter
most are the ones that must be easiest to test. Keep them that way.

## Open work

Nothing is open. Six changes were implemented and archived on 2026-08-16:
the read-only dashboard, the action contract's second version, the workload
actions, the observability bundle, launch readiness, and the escape hatches.

Next, in the order the risk says they should land:

1. **`blastRadius` guard** — `cooldown` and `maxPerHour` cannot express
   "never more than 20% of a workload's pods" or "never the last healthy
   replica". It must land **before** the node actions, not after: shipping
   destructive verbs before the guard that bounds them is the one sequencing
   mistake here that would be hard to walk back.
2. **Node actions** — `node.cordon`, `node.drain`, `node.uncordon`,
   `pvc.expand`. Highest risk in the catalogue; `node.drain` must evict
   through the Eviction API and honour PodDisruptionBudgets, and a drain
   that cannot finish inside its timeout is a failure, not a partial
   success.
3. **Slack bot with approval** — brings the identity model that makes
   `mode: approval` auditable, which is what the *careful* tier of actions
   is waiting for.

### Asked for by the owner, not yet designed

Recorded here so they survive a cold pickup. None has a change written yet.

- **Per-namespace posture.** `dryRun` is global today: one flag on the
  operator. The owner wants the combination — act in some namespaces, only
  report in others. This is probably the most valuable item on the list,
  because it is how people actually adopt a tool that holds write access:
  live in `staging`, dry-run in `prod`, until the reports earn the change.
  It needs a decision about where the setting lives — the chart, the
  strategy, or a `Namespace` label — and each answer has a different failure
  mode when the setting and the RBAC disagree.
- **Namespace filtering in the dashboard.** Straightforward; the pages
  already read everything they would filter.
- **Cluster filtering in the dashboard.** Implies hub/spoke, which is
  "Later" on the roadmap: today's operator sees one cluster because it runs
  in one.
- **Continuous capability checks with SLI/SLO output.** A workload that
  continuously exercises what a cluster can do — schedule a Deployment,
  reach an Ingress, egress, container runtime, etcd and API latency — and
  turns it into quantifiable service levels. Effectively a second product
  beside this one, and it deserves its own change and its own argument about
  what it measures. The nearest existing art is the Kubernetes e2e suite and
  synthetic monitoring; the interesting part is making the results a service
  level rather than a pass/fail.
