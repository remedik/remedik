## Context

The dashboard answers "is anything wrong right now" and "what happened to
this one". It does not answer "which namespace is this going badly in",
which is the question a team with fifty of them actually has.

## Decisions

### 1. Health means remedik's record of it, and the page says so

remedik sees the remediations it ran. It does not see whether the namespace
is healthy — it never measured that. A page called "namespace health" that
implied otherwise would be a dashboard being authoritative about something
outside its knowledge, which is the failure mode this project spends most of
its care avoiding.

So the page is about remediation: how often remedik acted here, how that
went, and whether anybody heard about the failures.

### 2. Ordered by what needs attention

Alphabetical is the ordering that requires reading everything. Failures
first, then failures nobody was told about, then volume, then name for
stability — so the top of the page is the part worth reading and the order
does not shuffle between refreshes.

### 3. Posture is on every row

With posture per namespace, two rows with the same failure count can mean
opposite things: one where remedik tried and failed, one where it only ever
reported. Showing the count without the posture invites exactly the wrong
conclusion.

### 4. Only namespaces remedik has touched

Listing every namespace in the cluster would need a permission the chart
does not grant, and the RBAC rule is that a permission exists because a
named feature needs it. A page of empty rows would also say nothing: a
namespace absent from this list is a namespace nothing has happened in,
which is the answer.

## Risks / Trade-offs

- **A namespace that was busy last month and quiet since still appears**,
  because history is what remedik has. The last-activity column is there so
  that reads as "quiet" rather than "current".
