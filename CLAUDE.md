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

The reader-facing version, with the reasoning and the edges, is
[docs/invariants.md](docs/invariants.md). Keep the two in step: that one is what
somebody reads before trusting this with a cluster.

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

   Its corollary: **a Content-Security-Policy violation is invisible to every
   test that does not run a browser.** This one has silently broken two
   features — `style-src` discarded inline styles and four bar charts rendered
   at full width for months; `form-action 'none'` blocked the filter's form
   and it was reported broken four times. Both were correct markup, correct
   handlers, correct server. If a page looks right and behaves otherwise,
   `hack/browser-check.mjs` reads the console, which is the only place the
   browser says so.
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

`openspec/changes/` — anything not under `archive/` is proposed and not built.
That directory is the answer, so this file cannot go stale about it.

## Traps

Five things cost real debugging and are invisible until they bite. Each has a
test; each test explains itself.

1. **Never retry the status write on conflict.** `Reconcile` reads through the
   manager's cache, so a second pass can see `Running` after the first recorded
   `Succeeded` and, by invariant 3, decide it was interrupted. The conflict on
   that write is what refuses the stale verdict — it is the check, not a defect
   to smooth over. `internal/engine/staleread_test.go`.
2. **`NeedLeaderElection() == false` on every HTTP server is load-bearing.**
   controller-runtime starts a runnable that says nothing only after the lease
   is won, so without it a standby has no listener and refuses the connection
   rather than answering 503. `cmd/remedik/main_test.go`.
3. **Readiness is not leadership.** Gating it made a standby never ready, so
   `helm --wait` never finished with two replicas.
4. **A Content-Security-Policy violation is invisible to every test that does
   not run a browser.** This policy has silently broken two features:
   `style-src` discarded inline styles and four bar charts rendered at full
   width for months; `form-action 'none'` blocked the filter's form and it was
   reported broken four times. Correct markup, correct handler, correct server,
   every test green. `hack/browser-check.mjs` reads the console, which is the
   only place the browser says so.
5. **Escalation steps are not a plan.** They are alternative ways to reach a
   person, so one failing must not skip the rest — the opposite of the rule for
   a remediation's own steps, which do act on each other's results.

The method is the transferable part: reapply on a branch, instrument, run
`make e2e`, and read the log rather than theorising. It is deterministic, and
it is what found all of these.

## Wanted, not yet designed

Recorded so they survive a cold pickup.

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
- **`ActionPlugin`.** `job.run` already runs any image as any ServiceAccount,
  so a custom action needs no code today. What is missing is a *typed* one,
  with its own parameters, validation and declared RBAC. Worth designing after
  seeing what people actually ask for, not before: a plugin mechanism inside
  something holding cluster write access is a trust surface, and guessing at
  it is how that surface ends up wider than anybody wanted.

## Repository settings, which are not code

`hack/github-setup.sh` applies what the API allows and reports the rest on
every run, so the state is checkable rather than remembered. Two things it
cannot do:

- **Requiring two-factor authentication.** The org endpoint accepts the field
  and silently ignores it — it is documented as a response, not a parameter —
  so it is a UI setting at
  `https://github.com/organizations/remedik/settings/security`.
- **The social preview image.** `docs/brand/remedik-banner.png` is 1280x640
  and ready; Settings is the only way to upload one.
