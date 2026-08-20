## Context

The dashboard is server-rendered Go templates, one stylesheet, one script that
is pure enhancement, and a filter that is entirely in the URL. Every decision
below is constrained by four things already settled and worth keeping:

1. **The dashboard never writes** (invariant 8). Both layers.
2. **State lives in the URL, not in a control.** Anything held between a click
   and its effect is destroyed by the ten-second refresh; that bug has shipped
   twice.
3. **The cost of a page is linear in the records**, and benchmarks say so.
4. **A CSP violation is invisible to every test that does not run a browser**
   (trap 4). Two features have already been broken silently by this policy.

The competitive read that produced this change is worth stating, because it
also says what *not* to build. Robusta correlates alerts with cluster changes
on a timeline; Komodor closed the loop with buttons, and needed RBAC, SSO and
an audit trail to be allowed to; Datadog gives every workflow run a debugger
with per-step input and output; Keep, Rundeck and Tines all sell a visual
builder. The first two ideas are worth having in our own terms. The last one
is the opposite of this project: strategies are declared in git and reviewed,
and a drag-and-drop editor for cluster-write authority would undo that.

## Decisions

### 1. Grouping is presentation, and only where adjacency means something

A group row stands for records that are adjacent in the current order and
share strategy, target, alert and state. Adjacency is the whole trick: it
makes grouping a single pass over the page's rows, with no map, no second
query and no change to the counts above the table.

It follows that grouping is enabled only in the default time order. Sorted by
duration, "adjacent" means nothing, and a group would be an arbitrary subset
presented as a run. Choosing another sort turns it off, and the page says so
rather than silently changing what a row means.

The counts above the list keep counting *records*, never groups, and the group
row states its own multiplicity. A page that says "1–50 of 1328" while
displaying 31 rows is lying in the one place a reader is counting.

Rejected: server-side deduplication with a `?group=` parameter. It would have
to page over groups, which makes "how many records match" and "how many rows
are shown" two different numbers that both look like the answer.

### 2. The approvals page is the deadline, sorted

`/approvals` exists because `AwaitingApproval` is the only state with a clock.
Every other state is history; this one expires, and when it expires the
remediation fails and escalates. Sorting it by age — which is what the list
does — puts the record with fourteen minutes left above the one with forty
seconds.

The countdown is server-rendered and refreshed by the existing ten-second
poll. A JavaScript timer would tick more smoothly and would also be a second
source of truth about time, capable of showing "2m left" on a record the
operator already timed out. Ten-second granularity on a fifteen-minute
deadline is not the constraint anybody thinks it is.

A deadline in the past is shown as expired rather than as a negative number:
the reconcile that fails it may not have happened yet, and the honest reading
is "this is over, the record has not caught up".

The page shows what *would* run — the resolved steps — because approving
something whose effect is on another page is how a person approves the wrong
thing at 03:00.

Nav carries the count. An empty queue still has a page, and that page names
the reason it is empty: no strategy uses `mode: approval` is a different
answer from every approval having been decided.

### 3. Impact is derived, or it is not shown

Three numbers, each defined as a sentence a reader can check against the
records:

- **Handled without a person** — executions that ran for real and succeeded,
  over executions that ran for real. Simulated records are excluded, because
  nothing happened. This is the number the industry calls automation coverage,
  stated as what it actually measures here.
- **Median alert to outcome** — from the alert's `firingSince` to the
  record's completion. Median, not mean: one interrupted record that sat for a
  day would otherwise move a figure describing ninety seconds.
- **Direction** — the same two figures over the previous window of equal
  length, shown as the delta. Without it a percentage is a mood.

A window with too few records to be worth a median says so instead of printing
one. Retention prunes records, so a 7-day window may be describing less than
seven days; the panel states the range it actually covered.

Rejected outright: "time saved", "MTTR reduced by", "incidents avoided". Every
one of them needs a counterfactual remedik cannot observe.

### 4. The activity chart stacks, and gains a range

Three stacked segments per bar — succeeded, failed, simulated — in the same
three colours the rest of the pages already use for those states, so no legend
is needed to read it. 24h and 7d are links, like every other choice on these
pages.

The floor added in `rework-dashboard-pages` stays: a peak of one must not draw
twenty-four full bars. The `title` on each bar carries the exact numbers,
which is a tooltip that costs no JavaScript and works on a keyboard.

### 5. The timeline is a sequence with elapsed time, not a Gantt chart

Timestamps have second granularity and most remediations complete inside one
second. A proportional bar chart of those durations would draw a single
rectangle and imply a precision the data does not have.

So the timeline is an ordered sequence — alert firing, record created, waiting
for approval, attempt *n*, each step, escalation — with the elapsed time
between entries, and a proportional bar drawn only for entries at least a
second long. Where everything is instant, the timeline says the whole thing
took under a second, which is itself the answer to "was it slow".

Beside it: **this target, before.** The last few records for the same target,
as outcome marks with ages. It reuses the `target` filter added in the working
tree, so the panel is a summary of a page that already exists.

### 6. The explainer is a table of rules, and it shows its work

A record carries a reason, a failing step, the message that action returned,
the strategy's mode and guards, the posture in force, and the history of the
same target. Those are enough to say something better than the raw error for
the cases that actually occur:

- `not found` after a `deployment.restart` — the object named by the alert's
  labels does not exist; the alert's `namespace` and the target's disagree, or
  the workload has been deleted since the alert fired.
- `forbidden` — the step names a ServiceAccount that lacks the verb, and
  invariant 7 says remedik's own is refused on purpose.
- `UnknownAction` — the strategy names an action this build does not have.
- `Interrupted` — the process died mid-attempt; by invariant 3 it is never
  resumed.
- `ApprovalTimeout` — nobody decided in time, which is silence, not refusal.
- The same target failing repeatedly — remediation is not the fix here.

Each rule states the fields it read and renders as one sentence of cause plus,
where there is one, the next command. It is a pure function of the record: no
clients, no clock beyond the one passed in, and a test per rule.

This is where every competitor puts an LLM. A rule that cites its inputs can
be checked by the person it is talking to; a generated sentence cannot, and
would put an outbound connection into a binary whose lack of one is documented
as a security property. The set of rules is small on purpose — when a case is
not recognised, the page shows the raw message it always showed, and says
nothing rather than guessing.

### 7. The palette is an enhancement, and the shortcuts are too

`Ctrl/⌘+K` opens an overlay over a list the server already computes: the
pages, the namespaces with counts, the strategies, the alert names, and the
most recent records. It is fetched once from `/palette`, a GET route returning
the same shape the filter controls are built from, and filtered in the browser
as the reader types. `g` then `o`, `r`, `n`, `s`, `a` reaches the five pages;
`?` lists the keys; `Escape` closes.

Every one of these is an enhancement in the strict sense: with JavaScript off,
the nav links are the navigation and nothing is missing. The palette navigates
by setting `location`, so a chosen result is still a URL.

The fetch is same-origin, which the auto-refresh already established. The
overlay is a `<dialog>` styled by the stylesheet — no inline style attribute
survives this CSP, and that is exactly how four bar charts were once rendered
at full width for months. `hack/browser-check.mjs` reads the console on this
page, or it does not ship.

### 8. Deep links are configuration, and hostile configuration is assumed

`dashboard.links` in the chart is a list of `{name, url}`. The URL is a
template over `{namespace}`, `{target}`, `{name}`, `{alert}`, `{fingerprint}`,
`{from}` and `{to}` — the last two an ISO window around the record, which is
what a Grafana or a Loki link needs.

Values are percent-encoded on substitution, only `http` and `https` schemes
are accepted, and anything else is dropped at startup with a log line. The
person writing values.yaml is trusted with the cluster, but a link is rendered
into a page and an unchecked scheme there is a `javascript:` URL waiting for a
reader who trusts the dashboard.

### 9. Cards are the same markup, viewed differently

Below 720px each table row becomes a card: the header cells stop being a row
and each data cell renders its column name from a `data-label` attribute. One
set of markup, one media query, no duplicated template and no JavaScript
deciding what device this is. The header wraps, the posture chip stays on its
own line, and the palette is not offered where there is no keyboard.

## Risks / Trade-offs

- **Grouping can hide the second distinct thing.** A group of twelve is one
  row where twelve were before, and the thirteenth record is now visible
  sooner — which is the point — but a reader scanning for a specific record
  will not see it inside a collapsed group. Mitigated by the group being
  expandable in place and by the count above the table still counting records.
- **The explainer will be wrong eventually.** A rule matching a message
  substring is a heuristic against text another project controls. Mitigated
  by never replacing the raw message, only adding beside it, and by saying
  nothing when no rule matches.
- **`/palette` is a new route on an authenticated surface.** It discloses
  exactly what the pages already do, to a request that already passed the same
  auth, and it is GET-only like everything else.
- **A fifth nav entry is a fifth thing to read.** Approvals earns it by having
  a deadline; nothing else proposed here gets a page.
- **7d activity may describe less than seven days** after retention prunes.
  The panel states its true range rather than implying the label.
