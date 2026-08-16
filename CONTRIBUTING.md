# Contributing

Thanks for considering a contribution! remedik is developed **spec-first** —
this file is the short version of the workflow.

## Ground rules

- **English everywhere**: code, comments, docs, commit messages, issues.
- **Spec-driven**: no behavior change without an approved spec. We use
  [OpenSpec](https://github.com/Fission-AI/OpenSpec): current truths live in
  `openspec/specs/`, proposals in `openspec/changes/<name>/`.
- **No assumptions**: external interfaces (Kubernetes, Slack, cloud APIs)
  are verified against current documentation before implementation.

## Workflow

1. **Propose**: `openspec new change "<kebab-name>"`, then fill proposal →
   specs → design → tasks (`openspec status --change <name>` guides the
   order; `openspec validate --change <name> --strict` must pass).
2. **Review**: get the proposal approved before writing code.
3. **Implement** in a branch; the PR template asks for the change reference.
4. **Definition of done** — all of:
   - `make verify` passes (gofmt, vet, golangci-lint, yamllint, helm lint,
     unit tests with race detector)
   - integration/e2e updated when the behavior is observable in a cluster
   - docs updated (README / QUICKSTART / docs/)
   - `CHANGELOG.md` updated under `[Unreleased]`
5. **Archive** the change after merge (`openspec archive`) so
   `openspec/specs/` stays the source of truth.

## Testing

Three layers, and the boundary between them is deliberate.

**Unit tests** cover every action, including — especially — every refusal.
The packages marked "stdlib only" in the layout exist so that the decisions
that matter most are the easiest to test: matching, guards and alert parsing
need no cluster, no fakes and no fixtures.

**`make e2e`** builds a throwaway kind cluster and runs the whole loop:
alert in, remediation out, on real objects. It covers thirteen of the
fourteen actions, and the reason to run it is that it catches the class of
bug unit tests cannot. The events-API migration is the example: every unit
test passed while every Kubernetes event was silently rejected, because the
RBAC named the old API group. Nothing short of a cluster finds that.

**What kind cannot host**, stated plainly rather than left as a gap:

- **A successful `pvc.expand`.** kind's `standard` StorageClass does not set
  `allowVolumeExpansion`, so the e2e tests the refusal — which is the
  behaviour worth guaranteeing, since the API server would otherwise accept
  the patch and change nothing. A successful expansion needs a CSI driver
  that supports resize.
- **A real `node.drain`.** kube-proxy does not come up on kind worker nodes
  in every environment, and pods scheduled there cannot reach the API
  server, so a multi-node e2e fails for reasons that have nothing to do with
  remedik. The drain is covered by its dry-run plan against real pods —
  which exercises every branch of the skip classification — plus unit tests
  for the eviction loop, the 429 retry and the partial-drain failure. The
  reasoning is in `hack/e2e/kind.yaml`; please read it before adding a node.

**`hack/rbac-unchanged.sh`**, run by `make verify`, proves the chart grants
nothing with every action disabled, that each action grants only its own
rules, and that the dashboard grants nothing at all. It is the mechanical
form of invariant 4.

**`hack/openspec-check.sh`**, also run by `make verify`, checks that the
spec-first workflow was followed rather than trusting that everyone
remembered.

Definition of done: `make verify` passes, docs and `CHANGELOG.md` are
updated, and anything touching cluster behaviour has been through
`make e2e`.

## Style

- Go: `gofmt` and `go vet` clean; exported identifiers documented;
  table-driven tests preferred; errors wrapped with context.
- Commits: [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `docs:`, `chore:` …).
