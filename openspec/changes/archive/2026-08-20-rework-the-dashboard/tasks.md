## 1. The spec catches up with the working tree

- [x] 1.1 Ordering, the three new filter axes and the Copy control are in the delta
- [x] 1.2 `make specs` and the existing dashboard tests pass before anything new is added

## 2. The list stops repeating itself

- [x] 2.1 Grouping of adjacent identical records, one pass, time order only
- [x] 2.2 A group row: count, newest and oldest age, the count linking to its records
- [x] 2.3 Counts above the table still count records; the page says so
- [x] 2.4 Another sort turns grouping off and the page explains why
- [x] 2.5 Tests: grouping, the boundary cases, and the benchmark still linear

## 3. Approvals get a page with a clock

- [x] 3.1 `/approvals`: a fifth route, view and template, GET and HEAD only
- [x] 3.2 Ordered by deadline; expired reads as expired
- [x] 3.3 Each entry shows the steps that would run and the two commands
- [x] 3.4 The nav entry carries the count; the empty page names which empty it is
- [x] 3.5 Tests: ordering, the expired case, both empty cases, 405 on write

## 4. The overview concludes

- [x] 4.1 Impact: share handled without a person, median alert-to-outcome, both with direction
- [x] 4.2 A window too small for a median says so; the panel states its true range
- [x] 4.3 Activity stacked by outcome, with 24h and 7d as links, counts on each bar
- [x] 4.4 Tests: each figure against fixtures, including the withheld median

## 5. A remediation has a shape in time

- [x] 5.1 The timeline builder: firing, created, waited, attempts, steps, escalation
- [x] 5.2 Bars only above a second; the sub-second record says so instead
- [x] 5.3 "This target, before" — recent outcomes for the same target, each a link
- [x] 5.4 Tests: order, elapsed times, the instant record, a target with no history

## 6. The explainer

- [x] 6.1 A pure function over record and time, returning cause, cited fields and next command
- [x] 6.2 The initial rule set: not found, forbidden, UnknownAction, Interrupted, ApprovalTimeout, repeated failure on one target
- [x] 6.3 Unrecognised records produce nothing; the raw message is never replaced
- [x] 6.4 A test per rule, and one asserting no client, clock or network is reachable from it

## 7. Reaching the pages without the mouse

- [x] 7.1 `/palette`: GET-only, authenticated, disclosing only what the pages show
- [x] 7.2 The overlay: open, narrow as you type, navigate, close — enhancement only
- [x] 7.3 `g` shortcuts to the five pages and `?` for the list of keys
- [x] 7.4 Tests: the route's method and auth; `hack/js-test.mjs` for the narrowing
- [x] 7.5 `hack/browser-check.mjs` reads the console with the palette open — trap 4

## 8. Links out

- [x] 8.1 `dashboard.links` in values, plumbed to the handler
- [x] 8.2 Substitution with percent-encoding; scheme validated at startup
- [x] 8.3 Rendered on the detail page; absent when none is configured
- [x] 8.4 Tests: encoding, a hostile scheme rejected, pages render with no links configured
- [x] 8.5 `hack/rbac-unchanged.sh` — the chart grew values, not permissions

## 9. The phone

- [x] 9.1 Cards below 720px from the same markup, via column labels
- [x] 9.2 The header keeps posture, pause reason and cluster legible
- [x] 9.3 `hack/browser-check.mjs` at a narrow viewport: no sideways scroll, no CSP violation

## 10. Done

- [x] 10.1 `make verify` green, including the benchmarks and the JS tests
- [x] 10.2 `make e2e` — five pages render, filtering, ordering and grouping work, still GET-only
- [x] 10.3 Screenshots regenerated, `docs/` and `CHANGELOG.md` updated
- [x] 10.4 Archived into `openspec/specs/readonly-dashboard/` (by hand: the CLI is a convenience, and `hack/openspec-check.sh` is the gate)
