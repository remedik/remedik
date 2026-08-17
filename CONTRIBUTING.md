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

**`hack/browser-check.mjs`** drives a real Chrome through the DevTools Protocol
and reads its console. It is not in `make verify` — it needs a cluster and a
browser — and it is what to reach for when a page looks correct and behaves
otherwise, because that is the shape a Content-Security-Policy violation has.
The policy has silently broken two features that way; both times every other
test passed.

**`make js-test`**, which `make verify` runs, exercises the dashboard's script
against a stub DOM and a controlled clock. The script had no tests until the
filter's control was reported broken for the fourth time.

**`make dev-seed`** fills the dev cluster with 150 namespaces and around 1900
records, reproducible from a fixed seed. It is not a test — nothing asserts —
but it is where the dashboard's claims about scale get checked by eye, and it
is worth running before changing a page. It found four defects the first time
it ran, including a page that rendered 151kB and a heading that said 81 of
150 namespaces needed attention.

One limitation is deliberately not hidden: the API server sets
`creationTimestamp`, so every seeded record is as old as the run. The volume,
the spread and the outcomes are real; the ages are not.

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
