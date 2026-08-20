## Why

The dashboard answers "what did remedik do" well and "what should I do about
it" badly. Five things say so, and each is visible in a screenshot rather
than inferred:

**The list repeats itself.** Eight consecutive rows reading `pod-crashloop ·
KubePodCrashLooping · Failed` are one fact printed eight times. The page
spends its most valuable space — the top of the list, during an incident —
saying the same thing, and the second distinct fact is below the fold.

**A waiting approval is a filter value.** `AwaitingApproval` has no page. The
overview warns that approvals are accumulating and links into the list, where
they sit among everything else, sorted by age rather than by which one expires
first. The one state on this operator with a *deadline* is the one state with
no view of its own.

**The overview counts, but does not conclude.** 1328 / 639 / 214 / 475 are
inputs to the two questions somebody actually has — is this getting better or
worse, and how much of this did remedik handle without anybody. Both are
computable from the records already loaded.

**The activity chart is one colour.** Twenty-four bars, all red, against an
axis of four, labelled `peak 1/h`. A panel headed "Activity" that cannot
distinguish a quiet day from a bad one is decoration.

**Nothing here has a shape in time.** A remediation is a list of steps with
timestamps beside them; the alert that caused it, the wait for a person, the
attempts and the escalation are four sections of the same page and no reader
assembles them into an order. And the failure itself is a Kubernetes error
message quoted verbatim — `deployments.apps "checkout-api" not found` —
which names the symptom and not the cause.

Two more, from where the dashboard is read: it is read on a phone by whoever
is on call, at a size the tables were never laid out for; and it is a
dead end, with a fingerprint printed as text beside an alert nobody can click
through to.

## What Changes

Nine changes to `internal/dashboard`, no new dependency, no new RBAC rule, and
no new outbound connection.

- **Repeats collapse into one row that counts them.** Rows adjacent in time
  that share strategy, target, alert and state become a single row reading
  `×12 · newest 21m ago · oldest 7h`, expandable in place and linking to the
  filter that shows exactly those records. Grouping applies only when the list
  is in its default time order, because adjacency is only meaningful there.
- **`/approvals` — a page for the queue with a clock on it.** Everything in
  `AwaitingApproval`, soonest deadline first, each with the time left, what it
  will run if approved, and the two commands that decide it. The nav entry
  carries the count.
- **An impact panel on the overview**: what share of executions remedik
  finished without a person, the median time from alert to outcome, and both
  against the previous window so the number has a direction.
- **The activity chart carries outcome and range.** Bars stacked
  succeeded / failed / simulated, with 24h and 7d as links.
- **A timeline on the detail page** — firing, matched, waited, attempted,
  escalated — as one ordered sequence with real elapsed time, plus what
  happened to *this target* before.
- **An explanation beside the error.** A deterministic explainer turns a
  record's reason, failing step, message and history into one sentence of
  cause and one next command. It cites the fields it read, so it can be
  argued with.
- **A command palette and keyboard navigation.** `Ctrl/⌘+K` for anything
  nameable — page, namespace, strategy, alert, recent record — `g` then a
  letter for the pages, `?` for the list of keys.
- **Deep links out**, configured in the chart: Grafana, Prometheus,
  Alertmanager, or anything else, templated with the record's own namespace,
  target, alert and time window.
- **The tables become cards below 720px**, in CSS, from the same markup.

The spec also catches up with three things already in the working tree and
not yet in `openspec/specs/`: sorting by column, the `escalation`, `target`
and `alert` filter axes, and the Copy button on printed commands.

## Non-goals

- **Writes, of any kind.** The approvals page prints the commands; it does not
  send them. Invariant 8 is unchanged, both layers of it: a `client.Reader`
  makes a write impossible to call, the method allowlist makes it impossible
  to reach.
- **An LLM anywhere near this.** The explainer is a table of rules over fields
  the record already carries. The only outbound connection in the binary stays
  the API server, which is a documented property of installing this thing.
- **"Time saved".** Every vendor in this category multiplies incidents by a
  guessed engineer-hour. remedik does not know what a human would have taken,
  and a number nobody can derive is worse than no number.
- **A charting library, a bundler, or a framework.** Unchanged from
  `rework-dashboard-pages`: bars are CSS over numbers the server has.
- **Multi-cluster.** Still one operator, one cluster. See "The cluster is
  named, not filtered".

## Capabilities

### Modified Capabilities

- `readonly-dashboard`

## Impact

- `internal/dashboard`: a fifth route and view; grouping, timeline, impact and
  explanation as builders beside the existing ones; the stylesheet gains a
  card breakpoint; `app.js` gains the palette as an enhancement.
- `charts/remedik`: `dashboard.links` values, rendered into the operator's
  configuration. No RBAC change — `hack/rbac-unchanged.sh` proves it.
- `hack/browser-check.mjs`: the palette and the card breakpoint are exactly
  the kind of thing trap 4 hides, so both are checked in a browser.
