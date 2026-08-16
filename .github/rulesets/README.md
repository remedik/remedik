# Rulesets

`main.json` is the protection on the default branch, kept here rather than
clicked into the settings page.

The reason is the same one that put the chart's RBAC in a data file and the
dashboard's performance numbers in a benchmark: a rule nobody can review is
a rule nobody checks. This one is a diff, it goes through the same review as
the code it protects, and `hack/github-setup.sh` applies it.

What it says, and why each line is there:

| Rule | Why |
| --- | --- |
| `deletion`, `non_fast_forward` | `main` is what the release tags point into. Losing it or rewriting it is not something a mistake should be able to do. |
| `required_linear_history` | The history here is the argument for the code. Merge commits bury it. |
| `pull_request` with squash only | One commit per change, whose message is the pull request's body. |
| `required_review_thread_resolution` | A comment nobody answered is a review nobody finished. |
| `required_status_checks`: `verify`, `vuln`, `e2e` | The three jobs. `e2e` is included deliberately: it has caught two defects no unit test could, and a check that is skipped because it is slow is a check that stops being true. |
| `strict_required_status_checks_policy` | Green against an old `main` is not green. |

`required_approving_review_count` is **0**, which looks wrong and is not.
GitHub will not let an author approve their own pull request, so with one
maintainer any positive number blocks every change including security
fixes. The rule that matters — nothing reaches `main` without passing CI —
holds either way. Raise it the day there is a second maintainer, and add
`require_code_owner_review` with it.

## It is not applied yet

Rulesets need a public repository or a paid plan:

```
Upgrade to GitHub Pro or make this repository public to enable this feature.
```

This repository is private until `v0.1.0-rc.1` proves the release pipeline.
Run `hack/github-setup.sh` after making it public and this lands, along with
secret scanning and push protection, which are gated the same way.
