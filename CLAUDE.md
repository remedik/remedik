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

- **`openspec/specs/`** — the current behaviour contract, sixteen
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

   Its corollary: **never retry the status write on conflict.** Reconcile
   reads through the cache, so a second pass can see `Running` after the
   first recorded `Succeeded`; the conflict is what refuses that stale
   verdict. `internal/engine/staleread_test.go` guards it and explains it.
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
internal/dashboard/  Read-only web UI, five pages; templates and CSS embedded
internal/action/external/  webhook.call, job.run, script.run — the widest trust surface
charts/remedik/      Helm chart; RBAC assembled from enabled actions
hack/e2e.sh          The end-to-end test
```

The packages marked "stdlib only" are deliberate: the decisions that matter
most are the ones that must be easiest to test. Keep them that way.

## Open work

Nothing is open. `add-leader-election` and `add-namespace-health` were
implemented and archived on 2026-08-17.

The first one's history is worth two minutes before you touch the reconciler, because
three separate mistakes hid in it and none was visible without running the
whole loop:

1. **Never retry the status write on conflict.** `Reconcile` reads through
   the manager's cache, so a second pass can see `Running` after the first
   recorded `Succeeded` and, by invariant 3, decide it was interrupted. The
   conflict on that write is what refuses the stale verdict.
   `internal/engine/staleread_test.go` guards it.
2. **`NeedLeaderElection() == false` on every HTTP server is load-bearing.**
   controller-runtime starts a runnable that says nothing only after the
   lease is won, so without it a standby has no listener and refuses the
   connection rather than answering 503.
3. **Readiness is not leadership.** Gating it made a standby never ready, so
   `helm --wait` never finished with two replicas.

The method is the transferable part: reapply on a branch, instrument, run
`make e2e`, read the operator log rather than theorising. It is deterministic
and it found all three.

### Proven since

`release.yml` has run. `v0.1.0-rc.3` produced a multi-arch image, a cosign
keyless signature, an SBOM attestation and the chart pushed to OCI. The
repository exists at `github.com/remedik/remedik`, so the badges, the
chart's `icon:` and the advisory links resolve.

### Asked for by the owner

Recorded here so they survive a cold pickup.

**Delivered.** *Per-namespace posture* is `add-namespace-posture`, archived
2026-08-16: the posture is resolved once when the record is created and
written onto `spec.dryRun`, so every `Remediation` says which posture it ran
under and a later config change cannot rewrite history. *Namespace filtering
in the dashboard* is `add-dashboard-filters`, and `/namespaces` compares them.

**Still open, no change written:**

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

### Not a code change, and not doable from here

1. **2FA on the owner's GitHub account**, which is what blocks requiring it
   org-wide. `hack/github-setup.sh` reports it on every run.
2. **The repository's social preview image.** `docs/brand/remedik-banner.png`
   is 1280x640 and ready; it can only be uploaded through Settings.
3. **Making the repository public**, which unlocks rulesets, secret
   scanning, CodeQL, Scorecard and Pages. `hack/github-setup.sh` applies all
   of them on a re-run.
4. **A funding destination.** `.github/FUNDING.yml` is committed fully
   commented out, deliberately: a sponsor button leading nowhere is worse
   than none on a project asking to be trusted with cluster write access.
   `docs/funding.md` already says what the money would be for.
