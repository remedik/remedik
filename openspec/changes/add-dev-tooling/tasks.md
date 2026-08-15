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
