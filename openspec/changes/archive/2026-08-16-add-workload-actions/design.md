## Context

Three verbs, all reversible and scoped to one object. The interesting
decisions are not about what they do — that part is obvious — but about what
each refuses to do, and how the chart keeps track of what they are allowed
to touch once there are more than a handful.

## Decisions

1. **`pod.delete` uses the Eviction API, never DELETE.** Deleting a pod
   ignores PodDisruptionBudgets entirely; eviction is the only call that
   checks them, and returns 429 when the deletion would breach the budget.
   The cost of getting this wrong is taking down the last healthy replica of
   something during an incident that was already bad enough. A 429 is
   recorded as a refusal that names the budget, which is a better outcome
   than the deletion succeeding.

2. **`pod.delete` refuses a pod with no controller owner.** Nothing
   recreates a bare pod, so deleting one is not remediation, it is deletion.
   The refusal is the default rather than the only behaviour — `requireOwner:
   "false"` exists — because someone will have a legitimate reason and
   should be able to write it down in the strategy where a reviewer can see
   it.

3. **`deployment.restart` stays, and `workload.restart` is added beside
   it.** Renaming would break every strategy already written, for the
   benefit of a shorter action list. Both are the same implementation: one
   with its kind pinned, one that takes the kind from the alert or the step.

4. **The workload's kind is resolved from the alert's own labels.** The
   kubernetes-mixin alerts carry `deployment`, `statefulset` or `daemonset`
   as the label naming the object, so the label that is present *is* the
   kind. A strategy can still say `kind` and `name` explicitly, which is
   what an alert with none of those labels needs.

5. **No action guesses an owner from a pod name.** An alert about
   `api-7d9f8-x2k1` names a pod, and the Deployment behind it is *probably*
   `api`. Probably is not good enough to restart a workload at 3am; the
   alert must carry the label, or the strategy must name it. This was
   already true of `deployment.restart` and stays true of everything here.

6. **Verification asks whether the object is gone, not whether the call
   returned.** A deleted pod may linger through its grace period; a Job
   deletion propagates in the background. So `pod.delete` waits for the pod
   to disappear *or* be replaced by one with a different UID — a new pod
   with the same name is the successful outcome for a StatefulSet, and the
   UID is the only thing that distinguishes it from the old one still
   terminating.

7. **RBAC becomes a data file, not more branches.** The chart reads
   `action-rbac.yaml` and grants each action's rules only when that action
   is enabled. A reviewer checking invariant 4 — every permission exists
   because a named, enabled feature needs it — now reads one table instead
   of tracing nine conditionals through a template. The file sits beside the
   chart rather than inside `values.yaml` because it is not a value: nobody
   should be editing an action's permissions to make their strategy work.

## Risks / Trade-offs

- **Eviction can fail in a way DELETE would not.** That is the feature, and
  it will surprise someone whose PodDisruptionBudget is stricter than they
  remember. The refusal names the budget, and the message says what to do.
- **`workload.restart` widens what one action can touch** from Deployments
  to three kinds, so enabling it grants `patch` on all three. Someone who
  wants only Deployments keeps using `deployment.restart` and gets only that
  permission. The chart's table makes that trade visible.
- **A data-driven RBAC template is harder to read than a literal one** for
  someone who knows Helm but not this chart. Mitigated by the table being a
  plain YAML file with a comment per action, which is the artefact a
  security reviewer actually wants to be handed.

## Open Questions

None blocking. Whether `pod.delete` should retry a 429 itself, rather than
failing and leaving it to the retry budget, is worth revisiting once
`blastRadius` exists: at that point the engine knows how much disruption is
already in flight, and an action retrying blindly would be working against
it.
