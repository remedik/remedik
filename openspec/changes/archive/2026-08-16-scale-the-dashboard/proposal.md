## Why

The dashboard was built for the cluster in front of it. remedik is meant to
be installed by anybody, at any size, and two things break well before the
sizes that are ordinary in a large company.

Measured on 150 namespaces, 40 strategies and 10,000 records — a mid-sized
platform team, not an outlier:

| | |
| --- | --- |
| Filter values rendered | **190 pills** |
| Building the list page | **49.7 ms** |
| Building the overview | 0.96 ms |

The pills are unusable: 150 namespaces as a wall of links is not something
anybody filters by eye. And the 49.7 ms is quadratic, not merely slow — each
filter option counts itself with its own pass over every record, so the cost
is options × records. At 500 namespaces and 50,000 records it is seconds,
per page load, on the operator that is also running remediation.

## What Changes

- **Counting becomes one pass per dimension** instead of one per option.
  The result is identical; the cost stops being a product.
- **Filter controls adapt to how many values there are.** A handful stays
  pills — one click, no menu. Many becomes a select with every value and a
  quick-pick row of the busiest, so 150 namespaces is a control with
  built-in keyboard type-ahead rather than a wall.
- **The list pages.** `?page=N`, with links, because a page that draws 200
  rows and says 9,800 were not drawn is not a list of what happened.
- **The refresh replaces only the data.** The list page marks its live
  region, so the filter controls keep what the reader typed or chose. Making
  a select safe was the precondition for offering one at all.
- **Benchmarks and a stated limit**, so the next person changing this knows
  what it costs and at what size it stops being true.

## Non-goals

- **A namespaces page.** The filter's select covers choosing one; a page per
  dimension is a bigger idea and should follow evidence that people want it.
- **Server-side field indexes.** The overview builds in 0.96 ms over 10,000
  records from a warm cache, and pruning already bounds history. Indexing is
  the answer if that stops being true, and adding it now would be a guess
  dressed as engineering.
- **Infinite scroll.** Paging is navigation; scroll position is state.

## Capabilities

### Modified Capabilities

- `readonly-dashboard`

## Impact

- `internal/dashboard`: one-pass counting, adaptive controls, paging, a live
  region.
- No new dependency, no new request leaving the cluster, no new RBAC.
