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

- **`openspec/specs/`** — the current behaviour contract, four capabilities.
  This is authoritative; code that disagrees with it is a bug in one of them.
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
7. **English everywhere** in the repository: code, comments, docs, commits.

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
make verify        # gofmt, vet, golangci-lint, yamllint, helm lint, race tests
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
charts/remedik/      Helm chart; RBAC assembled from enabled actions
hack/e2e.sh          The end-to-end test
```

The packages marked "stdlib only" are deliberate: the decisions that matter
most are the ones that must be easiest to test. Keep them that way.

## Open work

`openspec/changes/add-readonly-gui/` — proposed, awaiting approval. Nothing
else is open.
