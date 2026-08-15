# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

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
