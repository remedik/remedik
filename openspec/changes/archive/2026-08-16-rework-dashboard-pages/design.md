## Context

The dashboard shipped as three pages, of which the first was doing the work
of three. Then a filter was added to it, twice, and did not work — for a
reason that had nothing to do with the filter.

## Decisions

### 1. Fix the stale shell first, because it hid everything else

`#content` is what the ten-second refresh replaces. `<head>`, the top bar and
the script tag are outside it. A tab open across an upgrade keeps the old
stylesheet, the old JavaScript and the old shell indefinitely, while its data
stays current — which is the most convincing way to be wrong, because the
page looks alive.

The refresh now compares the asset fingerprint in the fetched document with
the one the page is running and calls `location.reload()` when they differ.
The fingerprint already exists, on the stylesheet's query string, precisely
so an upgraded operator is never read through a cached copy of the old one;
it simply was not being checked.

This is also the answer to "why did two correct fixes not work": they were
correct, and the tab reading them was from before either.

### 2. Filtering is navigation, not a form

A `<select>` plus Apply holds state between the choice and the submission.
That state was destroyed by the refresh when the controls were inside
`#content`; moving them out fixed it, and left a design whose correctness
still depended on where the markup sits.

Links have no state. Clicking one navigates, which is the whole interaction.
There is nothing to lose, nothing to preserve across a swap, nothing to
reach before a timer fires, and no JavaScript involved at any point. It is
also fewer actions for the reader: one click instead of open, choose, aim,
click.

The cost is that a cluster with fifty namespaces would render fifty links.
That is a real limit and the honest answer is that it is not this cluster's
problem yet; when it is, the fix is a search box, which is also navigation.

### 3. The overview answers a question; the list answers a different one

"Is anything wrong right now?" and "what happened to payments last Tuesday?"
want different pages. Merging them produced a front page where the answer to
the first was three scrolls down, under a filter for the second.

The overview is now panels, each of which is a claim with a link to its
evidence: what needs attention, what the posture actually is, what has been
happening over the last day, and where. `/remediations` is the list, with
the filters, because that is what filters are for.

### 4. Panels are the unit of extension

Each panel is a struct built by one function and rendered by one template
block, and every panel that counts something links to the filtered list that
shows it. Adding "namespace health" or "approvals waiting" later is a
struct, a builder and a block — not a rearrangement of the page.

The same applies to pages: a route, a view, a template, one nav entry.

### 5. The activity chart is bars, drawn from numbers

Twenty-four buckets, one per hour, stacked by outcome. It is a `<div>` per
bucket with a height, and a `<table>` of the same numbers for anybody using
a screen reader. No library, no canvas, no request leaving the cluster —
which the page's own CSP forbids anyway.

## Risks / Trade-offs

- **A reload interrupts reading.** It happens only when the operator's build
  changes, which is rare and is exactly when the page should not be trusted.
  A pause button already exists for anybody who wants no movement at all.
- **More templates.** Four pages instead of three, and blocks within them.
  The alternative is one page that keeps growing, which is what this change
  is undoing.
