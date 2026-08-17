## Context

The operator was written for one instance and the chart sets one. Nothing
turns that into a guarantee, and the reason it matters is specific: the
guards hold their state in memory.

## Decisions

### 1. Leader election rather than a warning in the README

A second instance is a reasonable thing for somebody to try, and the failure
it produces is the one this project exists to prevent — two remediations for
one alert, with the cooldown that should have stopped the second sitting in
another process's memory.

Documentation does not stop that. A lease does.

### 2. The gateway answers 503, it does not stop listening

The obvious implementation is to start the gateway only on the leader. It is
wrong: the Service has one set of endpoints, so a non-leader with no listener
refuses the connection, and Alertmanager cannot tell "wrong pod" from
"remedik is down".

A 503 with a body saying which pod answered and that it is not the leader is
a normal outcome. Alertmanager retries a non-2xx, the Service picks a pod
again, and the alert lands. The cost is a delay measured in Alertmanager's
retry interval; the alternative is an alert dropped or a page nobody can
diagnose.

This is the one place where answering non-2xx is correct, and it sits beside
the rule that says the gateway answers 200 to anything it understood. Both
are about being honest with the sender: 200 means "I have it", 503 means
"ask again", and the difference is whether anything was recorded.

### 3. The lease permission is justified by the feature

Invariant 4 says the chart grants a permission only because a named, enabled
feature needs it. `coordination.k8s.io/leases` is granted because leader
election needs it, in the operator's own namespace only, and the RBAC test
covers it like every other rule.

### 4. Guard state stays in memory

Two instances could share cooldowns through the API, and that is a different
project with a different failure mode. The in-memory design is why the guard
package has no Kubernetes dependency and why its tests are the fastest in
the repository. Leader election makes the design correct rather than
compensating for it.

## Risks / Trade-offs

- **A leadership transition drops nothing but delays something.** The new
  leader rebuilds guard state from the `Remediation` resources before it
  accepts anything, which is the existing startup path. Alerts arriving in
  that window get a 503 and are retried.
- **Lease churn on a busy control plane.** The default renew and lease
  durations are controller-runtime's, which is what every operator in the
  ecosystem uses.
