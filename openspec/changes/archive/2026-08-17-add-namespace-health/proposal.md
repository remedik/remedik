## Why

remedik knows which namespace every remediation touched, what its posture
was, whether it worked and whether anybody was told. Nothing puts those
together per namespace.

The question a platform team asks is not "what happened" but "where is this
going badly". With posture varying by namespace, that question has a second
half: a namespace where remedik only reports and a namespace where it acts
are not comparable, and today the dashboard shows them side by side as if
they were.

The overview's "Where" panel counts executions per namespace and stops
there. It is a summary of activity, not an answer about health.

## What Changes

- **`/namespaces`** — one row per namespace remedik has touched: its
  posture, how many executions, how they ended, how many failures nobody
  was told about, and when it last did anything.
- **Sorted by what needs attention**, not alphabetically: failures first,
  then failures with no escalation, then volume. A list ordered by name is a
  list somebody has to read all of.
- Each row links to that namespace's executions, so the page is a way in
  rather than a dead end.
- The overview's "Where" panel links here.

## Non-goals

- **Health of the namespace itself.** remedik knows what it remediated, not
  whether the workloads are well. Claiming otherwise would be a dashboard
  that looks authoritative about something it never measured.
- **Namespaces remedik has never touched.** They would need a list
  permission the chart does not grant, and a page full of empty rows says
  nothing. The absence of a namespace here is itself the answer.

## Capabilities

### Modified Capabilities

- `readonly-dashboard`

## Impact

- `internal/dashboard`: a fifth page and its view.
- No new RBAC, no new read: every field comes from records already listed.
