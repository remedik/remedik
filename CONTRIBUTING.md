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

## Style

- Go: `gofmt` and `go vet` clean; exported identifiers documented;
  table-driven tests preferred; errors wrapped with context.
- Commits: [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `docs:`, `chore:` …).
