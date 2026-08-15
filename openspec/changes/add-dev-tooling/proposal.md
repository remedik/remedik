## Why

Code quality gates and a reproducible local cluster were requested before
the MVP lands: linters for Go and YAML, generated chart docs, and a one-command
dev environment where alerts can be seen end-to-end (Prometheus /
Alertmanager / Grafana UIs). Establishing these before feature code keeps
every future change lint-clean from its first commit.

## What Changes

- Extend the Makefile with pinned, self-installing tooling (into `hack/bin/`):
  `make lint` (golangci-lint v2.12.2), `make yaml-lint` (yamllint),
  `make yaml-fix` (yamlfmt v0.21.0), `make helm-lint`, `make helm-docs`
  (helm-docs v1.14.2), `make tools`.
- `make verify` now runs the full gate: gofmt, vet, golangci-lint, yamllint,
  helm lint, unit tests with race detector — identical to CI.
- Add lint configs: `.golangci.yml` (v2 config), `.yamllint`, `.yamlfmt`.
- Dev environment: `make dev-up` creates a kind cluster (`remedik-dev`) and
  installs kube-prometheus-stack (Grafana/Prometheus/Alertmanager);
  `make dev-info` prints UI access; `make dev-down` tears down. Config under
  `hack/dev/`.
- Chart docs generated with helm-docs from annotated `values.yaml`
  (`README.md.gotmpl`); CI fails if generated docs are stale.
- CI extended accordingly (helm + yamllint installed, pinned).

## Non-goals

- No runtime behavior changes; the remedik binary is untouched.
- No deployment of remedik into the dev cluster yet (`make dev-deploy`
  arrives with `add-mvp-core`).
- No AI/Ollama components in the dev cluster (revisit with the AI change).

## Capabilities

### New Capabilities

(none — developer tooling only; `skip_specs: true`)

### Modified Capabilities

(none)

## Impact

- Makefile, `.golangci.yml`, `.yamllint`, `.yamlfmt`, `hack/dev/`,
  `.github/workflows/ci.yml`, `charts/remedik/` (annotated values +
  generated README), QUICKSTART/CONTRIBUTING/CHANGELOG.
- Developers need `yamllint` and `helm` locally (`make` prints install
  hints); Go-based tools self-install pinned.
