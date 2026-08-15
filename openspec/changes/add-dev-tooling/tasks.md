## 1. Lint tooling

- [x] 1.1 Makefile: pinned tool versions + self-install rules into `hack/bin/` (`make tools`)
- [x] 1.2 Targets: `lint`, `yaml-lint`, `yaml-fix`, `helm-lint`, `helm-docs`; `verify` runs the full gate
- [x] 1.3 Configs: `.golangci.yml` (v2), `.yamllint`, `.yamlfmt`; repo passes all linters

## 2. Dev cluster

- [x] 2.1 `hack/dev/kind.yaml` + `hack/dev/monitoring-values.yaml`
- [x] 2.2 `make dev-up` / `dev-info` / `dev-down` with tool presence checks and install hints

## 3. Docs & CI

- [x] 3.1 Annotate `charts/remedik/values.yaml` for helm-docs; add `README.md.gotmpl`; generate README
- [x] 3.2 CI: install helm + yamllint (pinned), run `make verify`, fail on stale chart docs
- [x] 3.3 Update QUICKSTART (tooling + dev cluster sections), CONTRIBUTING (DoD), CHANGELOG

## 4. Version currency

- [x] 4.1 Audit every pinned version against upstream; bump Go to 1.26, CI actions to checkout v7 / setup-go v7 / setup-helm v5
- [x] 4.2 Pin previously floating versions (kube-prometheus-stack 88.3.0, kind v0.32.0, helm v3.21.4 in docs)
- [x] 4.3 Add `make versions` (`hack/versions.sh`) so drift is checkable on demand
