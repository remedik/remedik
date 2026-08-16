## Context

Three actions that change how much of something runs, or which version of
it. Each is easy to implement and easy to regret, so the design is mostly
about what they refuse.

## Decisions

1. **A rollback refuses on a GitOps-managed workload.** Argo CD and Flux
   reconcile continuously: a rollback lands, the controller notices drift,
   and the broken version is back within minutes. The operator sees remedik
   record `Succeeded` and the outage continue, which is worse than remedik
   doing nothing — it costs the incident the time it takes to work out that
   two systems are fighting. The refusal names the controller and the label
   that revealed it, and `ignoreGitOps: "true"` exists for somebody who has
   read that sentence and disagrees.

2. **A rollback reads the revision history rather than storing one.**
   `kubectl rollout undo` finds the ReplicaSet whose
   `deployment.kubernetes.io/revision` annotation is the one wanted and puts
   its pod template back. remedik does exactly that, so the two agree about
   what "the previous revision" means, and history stays bounded by
   `revisionHistoryLimit` — a number the workload's owner already chose.

3. **Scaling refuses when an autoscaler owns the workload.** Setting
   `replicas` on an HPA-managed Deployment is a change the autoscaler
   reverts on its next interval. Detecting it costs `list` on
   HorizontalPodAutoscalers, which is a read-only permission on a
   non-sensitive resource, and buys a refusal that explains itself instead
   of a remediation that silently does not stick. When there is an
   autoscaler, `hpa.scale` is the action that works.

4. **Every scale states a maximum.** `increaseBy` without a ceiling is an
   alert storm with a credit card. The maximum is required rather than
   defaulted, because a default here is a number this chart invented for
   somebody else's cluster and budget.

5. **There is no scale-down verb.** Every alert that reaches remedik says
   something is unhealthy, and "run less of it" is not an answer to that. A
   step can set an absolute `replicas` below the current count if that is
   genuinely the intent — it is visible in the manifest — but nothing
   invites it.

6. **Verification asks the question the action was for.** A scale is
   verified by the workload reporting the new count *available*, not by the
   spec having been updated: replicas that cannot schedule are not capacity.
   A rollback is verified by the rollout completing at the new generation,
   the same as a restart.

## Risks / Trade-offs

- **`deployment.scale` grants `list` on HorizontalPodAutoscalers
  cluster-wide.** Read-only, on a resource that holds no secrets, in
  exchange for a safety check that prevents a whole class of "the
  remediation did not stick" incidents. Stated in the RBAC table so a
  reviewer sees the trade rather than discovering it.
- **The GitOps check is heuristic.** It looks for the labels and annotations
  Argo CD and Flux actually set. A cluster using something else, or a
  workload those controllers manage without labelling, is not detected.
  That is a reason to keep `blastRadius` and dry-run in front of a rollback,
  not a reason to skip the check that catches the common case.
- **A rollback to a revision that was also broken.** remedik puts back the
  previous pod template; whether that version was good is not a question the
  cluster can answer. `toRevision` exists for an operator who knows which
  one was.

## Open Questions

None blocking. Rolling back StatefulSets and DaemonSets is a real request
waiting to happen; it needs its own change, because their rollouts are
ordered and their rollbacks are much harder to undo again.
