# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

Everything below is implemented and verified: `make verify` for the unit
suite, `make e2e` for the whole loop on a real cluster. Both OpenSpec
changes are archived, so `openspec/specs/` is now the current contract
rather than a proposal.

### Added

- **The MVP loop works end to end.** An Alertmanager delivery reaches the
  gateway, a strategy matches it, guards decide, the engine executes and
  records the outcome as a `Remediation` resource — installable with
  `helm install`, verified by `make e2e` on a real kind cluster.
- Remediation controller (`add-mvp-core` tasks 3.2–3.6): the execution state
  machine, with retries and exponential backoff, `Interrupted` recovery
  after a crash, and pruning of terminal records per strategy. Guard history
  is rebuilt from existing resources at startup, so cooldowns and hourly
  counts survive a restart.
- Alert sink: matches alerts to strategies, evaluates guards and creates the
  execution record. The plan and retry budget are copied onto the record, so
  it still explains the run after the strategy is edited or deleted.
- Guard rejections are published as Kubernetes events on the strategy, so
  `kubectl describe remediationstrategy` answers "why did nothing happen?"
  without anyone having to find the operator's logs.
- Prometheus metrics on the manager's endpoint: alerts received, truncated
  and unmatched, ingest errors, guard rejections, remediations started and
  finished by outcome, and execution duration.
- Helm chart: Deployment, Services, ServiceAccount and RBAC assembled from
  the actions actually enabled, a token Secret, and install notes carrying
  the exact Alertmanager receiver snippet. Dry-run is the install default,
  and the chart refuses to render an unauthenticated gateway unless asked.
- Container image: distroless, non-root, read-only root filesystem.
- `make e2e`: an end-to-end test on a throwaway kind cluster asserting
  authentication, dry-run leaving the workload untouched, a real restart,
  the cooldown refusing a repeat, an unmatched alert being ignored, and
  guards surviving an operator restart. Plus `make docker-build` and
  `make dev-deploy`.

- `deployment.restart` action (`add-mvp-core` task 4.2): rolling restart via
  the same `kubectl.kubernetes.io/restartedAt` annotation `kubectl rollout
  restart` uses, so the Deployment controller honours maxUnavailable,
  readiness and PodDisruptionBudgets. Never deletes pods.
- Step execution and retry timing (`add-mvp-core` task 3.2, partial):
  `StepRunner` sequences a strategy's steps, stops at the first failure and
  records the rest as Skipped; `Backoff` gives deterministic exponential
  retry delays capped at ten minutes.
- Action contract and registry (`add-mvp-core` task 4.1): every remediation
  verb implements Resolve / Plan / Execute, so dry-run calls Plan only and
  the mutating path is never reached. The registry rejects duplicate and
  empty action names and reports unknown actions with the list of known
  ones.
- API types (`add-mvp-core` task 1.1): `RemediationStrategy` (cluster-scoped)
  and `Remediation` (namespaced audit record) in `remedik.dev/v1alpha1`,
  with validation markers, print columns and status subresources. The
  package depends on k8s.io/apimachinery alone — not controller-runtime —
  so it stays cheap for clients and tools to import.
  `make generate` and `make manifests` produce DeepCopy code and CRDs with a
  pinned controller-gen; `make verify-codegen` fails on stale output.
- First cookbook entry: `examples/strategies/pod-crashloop.yaml`.
- Strategy selection and guards (`add-mvp-core` task 3.1): label-equality
  matching with most-specific-wins and deterministic tie-breaking, plus
  cooldown and maxPerHour guards that report which guard rejected an
  execution and when to retry. Includes an in-memory execution history for
  the engine to keep hot.
- Alert gateway (`add-mvp-core` task 2): HTTP receiver for Alertmanager
  webhooks with constant-time bearer authentication, body-size limits and
  normalization of grouped deliveries into individual alert events. Served
  by the binary on `:8090` (`--gateway-bind-address`, `--gateway-path`,
  token from `REMEDIK_GATEWAY_TOKEN`); alerts are logged until the
  remediation engine lands.

- Project scaffolding: spec-driven process (OpenSpec), architecture decision
  records, security policy, contribution guide, CI pipeline, Helm chart
  skeleton, and a minimal binary serving health/readiness probes.
- OpenSpec change `add-mvp-core` specifying the MVP: alert gateway,
  `RemediationStrategy`/`Remediation` CRDs, deterministic execution engine
  with guards and dry-run, and the `deployment.restart` action.
- Dev tooling (`add-dev-tooling`): golangci-lint, yamllint/yamlfmt and
  helm-docs Make targets with pinned tool versions installed into
  `hack/bin/`; `make dev-up`/`dev-down` for a local kind cluster with
  kube-prometheus-stack; CI extended to run the full lint suite and check
  generated chart docs.
- `make versions`: reports every pinned version against the latest upstream
  release, so drift is visible without hunting through files.

### Changed

- Minimum Go version is now 1.26 (matches the Kubernetes ecosystem, which
  controller-runtime requires).
- Pinned previously floating versions: kube-prometheus-stack 88.3.0, kind
  v0.32.0 and helm v3.21.4 documented as prerequisites.
- CI actions updated: `actions/checkout` v4 → v7, `actions/setup-go`
  v5 → v7, `azure/setup-helm` v4 → v5.
