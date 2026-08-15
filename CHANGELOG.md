# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

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
