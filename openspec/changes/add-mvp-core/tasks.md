## 1. API types & CRDs

- [ ] 1.1 Define `RemediationStrategy` and `Remediation` Go types (`api/v1alpha1`) with validation markers per spec
- [ ] 1.2 Generate CRD manifests + deepcopy (controller-gen); commit under `charts/remedik/crds/`

## 2. Alert gateway

- [x] 2.1 Webhook handler: bearer auth (constant-time, token from env/Secret), payload validation, event normalization per spec
- [x] 2.2 Telemetry seam: `Recorder` interface (received / truncated / ingest errors / unauthorized) with a no-op default; the Prometheus adapter is wired with the operator (task 3), where the unmatched counter also lives
- [x] 2.3 Unit tests: grouped payload split, 401 / 400 / 405 / 413 / 200 paths, auth variants, fingerprint derivation — 95%+ coverage on the gateway and alert packages
- [x] 2.4 Wire the gateway into the binary behind flags, with a logging sink until the engine exists

## 3. Engine

- [x] 3.1 Matching (most-specific-wins, deterministic ties) + guard evaluation (cooldown, maxPerHour) with table-driven tests — `internal/matching`, `internal/guards`, both 100% covered; includes `MemoryHistory` (the hot index the engine rebuilds from Remediation resources) with explicit, wall-clock-driven pruning
- [ ] 3.2 Remediation lifecycle: create → sequential step loop → terminal state; attempts + exponential backoff recorded in status
- [ ] 3.3 Global dry-run: Plan-only path producing `Simulated` records with the full would-have-run plan
- [ ] 3.4 Startup `Interrupted` sweep + terminal-record pruning (keep-last-N per strategy)
- [ ] 3.5 envtest coverage for the controller state machine

## 4. Action: deployment.restart

- [ ] 4.1 Action interface (`Validate` / `Plan` / `Execute`) + registry
- [ ] 4.2 `deployment.restart` implementation (restart-annotation patch) + unit tests incl. not-found and RBAC-denied paths

## 5. Helm chart

- [ ] 5.1 Templates: Deployment, ServiceAccount, Role/RoleBinding assembled from enabled actions, gateway Service; `dryRun` and gateway auth wired through values
- [ ] 5.2 NOTES.txt with the Alertmanager receiver snippet; `helm lint` added to CI

## 6. E2E & docs

- [ ] 6.1 kind e2e (`make e2e`): install chart → POST sample webhook → assert `Simulated` Remediation
- [ ] 6.2 Docs: QUICKSTART section 2 → "current", `docs/architecture.md` status labels, CHANGELOG entry
