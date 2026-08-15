## 1. API types & CRDs

- [x] 1.1 Define `RemediationStrategy` and `Remediation` Go types (`api/v1alpha1`) with validation markers per spec
- [x] 1.2 Generate CRD manifests + deepcopy (`make generate manifests`, controller-gen v0.21.0 pinned); committed under `charts/remedik/crds/`. CI enforces freshness via `make verify-codegen`. Schema verified to reject the dangerous shapes: no matchers, no steps, unknown execution mode, retries above the cap

## 2. Alert gateway

- [x] 2.1 Webhook handler: bearer auth (constant-time, token from env/Secret), payload validation, event normalization per spec
- [x] 2.2 Telemetry seam: `Recorder` interface (received / truncated / ingest errors / unauthorized) with a no-op default; the Prometheus adapter is wired with the operator (task 3), where the unmatched counter also lives
- [x] 2.3 Unit tests: grouped payload split, 401 / 400 / 405 / 413 / 200 paths, auth variants, fingerprint derivation — 95%+ coverage on the gateway and alert packages
- [x] 2.4 Wire the gateway into the binary behind flags, with a logging sink until the engine exists

## 3. Engine

- [x] 3.1 Matching (most-specific-wins, deterministic ties) + guard evaluation (cooldown, maxPerHour) with table-driven tests — `internal/matching`, `internal/guards`, both 100% covered; includes `MemoryHistory` (the hot index the engine rebuilds from Remediation resources) with explicit, wall-clock-driven pruning
- [x] 3.2 Remediation lifecycle: create → sequential step loop → terminal state; attempts + exponential backoff recorded in status. Waiting for a retry is `Pending`, never `Running`, which is what keeps "Running means interrupted" true
- [x] 3.3 Global dry-run: Plan-only path producing `Simulated` records with the full would-have-run plan. Dry-run also records cooldowns, so a report is never more optimistic than reality would have been
- [x] 3.4 `Interrupted` recovery + terminal-record pruning (keep-last-N per strategy). No separate sweep is needed: an attempt runs to completion inside one reconcile, so a record found `Running` can only have been interrupted. The record that just finished is never a pruning candidate
- [x] 3.5 Controller state machine covered by unit tests against an in-memory client (`internal/engine`, 93%): success, dry-run, interruption, retry-then-succeed, retries exhausted, unknown action, pruning, prune-failure isolation. envtest was **not** used: it needs downloaded control-plane binaries, and the behaviour it would add over these tests — real API validation and status subresource semantics — is what `make e2e` exercises against a real cluster. Revisit if the state machine outgrows the fake
- [x] 3.6 Guard history rebuilt from existing `Remediation` resources at startup, so cooldowns and hourly counts survive a restart

## 4. Action: deployment.restart

- [x] 4.1 Action interface (`Resolve` / `Plan` / `Execute`) + registry — `internal/action`, 100% covered, standard library only. Dry-run calls Plan and never Execute, so a Simulated remediation cannot mutate the cluster even if an action is buggy
- [x] 4.2 `deployment.restart` implementation (restart-annotation patch) + unit tests incl. not-found and RBAC-denied paths — `internal/action/workload`, 94% covered. Refuses to guess a Deployment from a pod name: the alert must carry a `deployment` label or the step must name one

## 5. Helm chart

- [x] 5.1 Templates: Deployment, ServiceAccount, ClusterRole/Role assembled from enabled actions, gateway and metrics Services, token Secret; `dryRun` and gateway auth wired through values. The chart refuses to render an unauthenticated gateway unless that is asked for explicitly
- [x] 5.2 NOTES.txt with the Alertmanager receiver snippet; `helm lint` plus a render of both auth modes runs in `make verify` and CI

## 6. E2E & docs

- [x] 6.1 kind e2e (`make e2e`): builds the image, installs the chart and asserts six behaviours end to end — authentication, dry-run leaving the workload untouched, a real restart, the cooldown refusing a repeat, an unmatched alert being ignored, and guards surviving an operator restart
- [x] 6.2 Docs: README and QUICKSTART rewritten for a working product, `docs/architecture.md` status labels and state machine, chart README generated, CHANGELOG entry
