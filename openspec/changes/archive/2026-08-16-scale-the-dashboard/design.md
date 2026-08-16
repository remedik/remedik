## Context

The dashboard was written against a kind cluster with four strategies. The
owner's intent is a tool anybody can install, so the sizes that matter are
the ones a platform team has: hundreds of namespaces, tens of strategies,
history bounded only by pruning.

Both defects were found by measuring rather than by reasoning, which is why
they are stated as numbers in the proposal.

## Decisions

### 1. Count once per dimension, not once per option

`buildOptions` asked "how many records match this filter?" for every value
it offered, and each answer scanned every record. 195 options over 10,000
records is 1.95 million comparisons to draw one page.

The same numbers come from one pass: for each record, if it satisfies the
*other* clauses, increment the bucket for its own value. Three passes, one
per dimension, and the counts are identical — this is a rewrite of the
arithmetic, not a change to what is shown.

The lesson worth keeping is that the slow version read as obviously correct.
It was the benchmark that made it obviously wrong.

### 2. The control follows the cardinality

Pills are the best control for a handful: one click, everything visible, no
menu to open. They are the worst control for 150, which is a wall nobody
scans.

Above a threshold the dimension becomes a `<select>` with every value and
their counts, plus a quick-pick row of the busiest few. A select is not a
compromise here — browsers give it keyboard type-ahead for free, which is
exactly the "find my namespace among 150" interaction, and it costs no
JavaScript to get.

Both forms are still navigation. The pills are links; the select submits a
GET that lands on the same URL a link would have.

### 3. The refresh replaces only the data region

A select and a submit button hold state between the choice and the
submission — the same state whose destruction made the filter appear broken
twice. It could only be offered once that was structurally impossible.

The list page marks its rows and counts as the live region; the refresh
swaps that and leaves the controls alone. Pages with no live region keep
swapping their whole content, so nothing else changes.

The filter's options can then go stale until a navigation — a namespace
first seen thirty seconds ago is not in the select yet. That is the cheaper
side of the trade by a wide margin, and any filter click refreshes them.

### 4. Paging, not a cap

"200 shown, 9,800 not drawn" is not a list of what happened; it is a
truncation with an apology. Pages are links, so they compose with the
filters, survive a refresh, and can be sent to somebody.

The page size stays generous — the cost is rendering, and the numbers say
rendering a hundred rows is not what was slow.

### 5. The limit is written down

The proposal's table is the measurement, and the benchmarks that produced it
are checked in. A performance claim with no benchmark is a claim somebody
will contradict by accident.

## Risks / Trade-offs

- **A select is a form**, which this project has been burned by twice. It is
  safe here only because of decision 3, and the tests assert that the live
  region exists — if somebody removes it, the select breaks the same way.
- **Two controls to maintain** instead of one. The threshold is a constant
  with a comment, and both render from the same data.
