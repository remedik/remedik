## Context

The overview shows everything. During an incident somebody wants one
namespace, and today they use the browser's find-in-page.

## Decisions

### 1. Query parameters, because the alternative could not exist

The dashboard answers anything but GET and HEAD with 405 before routing, and
its handler is built from a `client.Reader`. A filter stored server-side
would need a write; a filter in `localStorage` would need JavaScript to be
the source of truth for what the page shows.

Query parameters are what is left, and they turn out to be what you would
have chosen anyway: a filtered view is a link, which is exactly what somebody
wants to hand to whoever is on call. The controls are a plain form, so they
work with JavaScript disabled, and the auto-refresh re-fetches
`window.location.href`, so the filter survives it without any extra code.

### 2. The counts follow the filter; the choices do not

Two rules that look inconsistent and are not.

The stats describe what is in the table. A page showing "Failed: 3" above a
list of one failure would make somebody believe two records were being
hidden by pagination rather than by their own filter.

The controls list every value present in the *unfiltered* records. A control
whose options shrink as you use it is one you can get stuck in — pick
`namespace=payments`, and if the strategy list then only offered strategies
seen in payments, switching to a different strategy would silently mean
"and clear the namespace".

### 3. The controls live outside the refreshed region

Found by using it rather than by curling it: the auto-refresh replaces the
contents of `#content` every ten seconds, and the controls were inside. A
selection made and not yet applied was destroyed on average within five
seconds — faster than anybody reaches the Apply button — so the filter
appeared not to work at all.

The first fix carried the pending selection across the swap in JavaScript.
It worked and it was untestable from here, and it made a filter's
correctness depend on an enhancement the page is supposed to survive
without. Rendering the controls above `<main>` makes the failure impossible
instead, and deleted the JavaScript that had just been written for it.

The cost is that the options do not gain a namespace first seen since the
page loaded, until it is reloaded. That is the cheaper of the two failure
modes by a wide margin.

### 4. An unknown value is kept, not rejected

`?namespace=does-not-exist` renders "nothing happened there", which is an
answer. Rejecting it with a 400 would turn a URL somebody pasted from a
week-old incident channel into an error page.

### 5. A namespace filter excludes cluster-scoped records

A node is in no namespace. Including drains in "everything in payments"
would make the list contain something that is not in payments, which is a
worse failure than omitting it — the reader has no way to notice.

### 6. The controls appear only when there is a choice

With one namespace, one strategy and one state, a filter row is furniture on
a page whose whole job is to be scanned quickly.

### 7. `clusterName` is a label, not a filter

remedik sees one cluster. A cluster control would be a select with one
option, which is a promise the tool cannot keep. A name in the header and
the tab title solves the real version of the problem — telling three
port-forwarded dashboards apart — and costs one value.

## Risks / Trade-offs

- **Filtering happens in memory, after listing everything.** At the scale
  this dashboard is for — a namespace of `Remediation` records, pruned to
  200 per strategy — that is cheaper than a field selector and needs no
  index. If a cluster ever holds enough records for it to matter, the fix is
  paging, not a cleverer filter.
