## Why

Two problems, one of which explains the other.

**The filter did not work, and neither of the two fixes reached anybody.**
The stylesheet, the script and the page shell live outside `#content`, and
the auto-refresh replaces only `#content`. A tab left open across an
operator upgrade therefore keeps the old assets and the old shell for ever,
auto-refreshing its data through them. Every fix shipped into that tab was
invisible. That is a real defect in its own right — after any upgrade, an
open dashboard renders new data through old markup — and it is why "the
filter still does not work" was the correct report each time.

**The overview does too much.** It carries the stats, the dry-run trial
report, the filter controls and a fifty-row table. It is the first page
anybody sees and it reads as a list with decoration rather than a dashboard,
so the answer to "is anything wrong right now?" has to be assembled by
scrolling.

## What Changes

- **The shell reloads itself when the operator changes.** The refresh
  compares the asset fingerprint it fetched with the running one and does a
  full reload when they differ, so an upgrade cannot leave a tab running
  last week's page.
- **Filtering becomes navigation.** Every choice is a link. There is no
  pending selection to lose, nothing for the refresh to destroy, and no
  Apply button to reach in time — one click filters. It works with
  JavaScript off, in a stale tab, and in any browser.
- **The overview becomes a dashboard**: posture, what needs attention now,
  activity over the last day, and where remediation is happening — each
  panel linking into the list it summarises.
- **`/remediations` becomes its own page**: the full list, the filters, and
  the counts. It is where somebody goes to look through history, which is
  not what a front page is for.
- The page shell gains the structure to make a fourth page cheap: one nav
  entry, one handler, one template using the same cards, tables and chips.

## Non-goals

- **A charting library.** The activity panel is bars in CSS from numbers the
  server already has. Adding a bundler to draw twenty rectangles would cost
  more than it is worth and put a request outside the cluster on a page that
  deliberately makes none.
- **Server-side paging.** The list caps at what the operator keeps per
  strategy; if that ever becomes too much to render, paging is the answer,
  not a smaller cap.
- **Writes.** Unchanged: the handler is built from a `client.Reader` and
  answers anything but GET and HEAD with 405 before routing.

## Capabilities

### Modified Capabilities

- `readonly-dashboard`

## Impact

- `internal/dashboard`: a fourth route and view; the overview rebuilt; the
  filter re-expressed as links; the stylesheet reorganised into components.
- No new RBAC, no new dependency, no new asset fetched from anywhere.
