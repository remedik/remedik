## Why

The overview lists the fifty most recent executions across every namespace.
That is the right answer to "what has remedik been doing" and the wrong one
to the question people actually arrive with during an incident, which is
"what has been happening in *payments*".

It gets worse as the tool succeeds. A cluster where remedik is working has
more records, not fewer, and the list becomes the thing you scroll past on
the way to the one page you wanted.

## What Changes

- **Filter the overview by namespace, strategy and state**, as query
  parameters. The dashboard allowlists GET and HEAD before routing, so this
  is the only shape a filter could take here — and the consequence is the
  useful part: a narrowed view is a URL somebody can paste into an incident
  channel.
- The counts above the table follow the filter. Numbers that disagreed with
  the rows beneath them would be worse than no filter.
- The controls' choices do not follow the filter, so a selection can always
  be changed or undone.
- **`clusterName`** — a label in the header and the browser tab. Not a
  filter; see the non-goals.

## Non-goals

- **A cluster filter.** remedik watches the cluster it runs in, so a control
  offering a choice of clusters would offer a choice of one. Filtering
  across clusters is the hub/spoke change. The real problem today is
  smaller and is solved by a name: three port-forwarded dashboards produce
  three identical-looking browser tabs.
- **Saved views, or any server-side state.** The URL is the saved view.
- **Filtering the strategy list.** Strategies are cluster-scoped and there
  are few of them; a control there would be furniture.

## Capabilities

### Modified Capabilities

- `readonly-dashboard`

## Impact

- `internal/dashboard`: a `Filter` and its options, applied before the view
  is built; a `<form method="get">` above the table.
- `charts/remedik`: `clusterName`.
