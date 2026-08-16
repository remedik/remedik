## Context

The dashboard is the first part of remedik a person who did not install it
will see, and it ships inside an operator that holds write access to a
cluster. Both facts push the same way: keep it small, keep it read-only,
and keep it from depending on anything outside the binary.

## Goals / Non-Goals

- **Goals**: answer "what would this have done?" and "why did nothing
  happen?" in a browser; add no permissions; add no build toolchain.
- **Non-goals**: writes of any kind, charts, multi-cluster, SSO — see the
  proposal.

## Decisions

1. **Server-rendered HTML with `html/template`, embedded via `go:embed`.**
   A single-page app would bring npm, a bundler and a second release
   artifact into a Go repository, for a dashboard whose whole job is to
   render a handful of lists. Server rendering keeps the deliverable one
   static binary, and `html/template` escapes by default — which matters,
   because every value shown comes from alert labels an operator does not
   control.

2. **A separate port and Service, not the metrics endpoint.** Metrics are
   scraped by Prometheus and should stay reachable by it; the dashboard is
   read by people and may be exposed differently. Separate ports let the
   cluster's owner apply different NetworkPolicies to each.

3. **Read-only enforced by the type system, not by discipline.** The
   handler is constructed with a `client.Reader`, not a `client.Client`, so
   there is no write method to call by accident. A method allowlist
   (GET/HEAD) is the second layer.

4. **Disabled by default, ClusterIP only, no Ingress in the chart.** The
   dashboard discloses alert labels, namespaces and workload names.
   Deciding who can see that is the cluster owner's call, and a chart that
   quietly published an Ingress would be making it for them. Documented
   access is `kubectl port-forward`; an optional bearer token covers the
   case where someone puts it behind their own gateway.

5. **Reads through the manager's cache.** The dashboard lists exactly what
   the reconciler already watches, so serving a page costs no API calls and
   needs no new RBAC. An expensive dashboard would be a denial-of-service
   vector against the operator's real job.

6. **Refresh by replacing the page's content, not by reloading it.** A
   dozen lines of dependency-free JavaScript re-fetch the current page and
   swap the main element, so an operator watching an incident does not lose
   their scroll position every ten seconds. Without JavaScript the page
   still renders; it simply does not refresh itself.

7. **Pagination by a hard cap, not by pages.** The overview shows the most
   recent 50 executions and says so. History is already bounded by pruning,
   and a dashboard that could render thousands of rows would be a way to
   make the operator slow.

## Risks / Trade-offs

- **Disclosure.** Alert labels can carry sensitive names. Mitigated by
  being off by default, ClusterIP-only, optional token, and by documenting
  the exposure plainly rather than burying it.
- **Server-rendered pages age.** If the dashboard later needs live updates
  or interactivity, this design is a rewrite rather than an extension. That
  is an acceptable trade for shipping something small now; the decision is
  recorded so the cost is visible when it is paid.

## Open Questions

None blocking. Whether the dashboard eventually gains write actions is a
question for the Slack change, which introduces the identity model that
would make an approve button auditable.
