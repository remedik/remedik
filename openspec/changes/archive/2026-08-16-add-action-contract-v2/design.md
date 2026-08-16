## Context

Twelve actions are about to be written. The contract they are written
against is therefore worth more attention now than at any later point: a
gap fixed here is fixed once, and the same gap fixed after the catalogue
exists is fixed twelve times, in twelve pull requests, against code people
already depend on.

## Goals / Non-Goals

- **Goals**: a step that can say whether it worked; an explanation visible
  on the object that changed; structured room for what each action knows.
- **Non-goals**: new actions, a mandatory verify, retry semantics.

## Decisions

1. **`Result` struct instead of more return values.** `Plan` and `Execute`
   return `(Result, error)` rather than growing to `(string, string,
   map[string]string, error)`. Adding a field to a struct is a compile-safe
   change for implementations that do not set it; adding a return value is
   not. The next action to need something the contract does not have should
   be able to add it without touching the eleven that came before.

2. **`Verify` is a separate optional interface, not a fourth method on
   `Action`.** `node.cordon` has nothing to verify — the cordon either
   applied or errored — and forcing it to implement a check would produce a
   method that returns success unconditionally, which is worse than no
   check at all because it looks like one. A type assertion is the Go way to
   say "some implementations can do more".

3. **`Verify` runs only after `Execute`, never in dry-run.** Verifying a
   plan that was not executed reports the state the cluster was already in,
   which reads on the page as though the remediation achieved it. Dry-run
   already answers a different question and should not borrow this one's
   vocabulary.

4. **A failed verify fails the step.** The alternative — succeed, but
   record that verification failed — creates a third outcome the state
   machine does not have and an operator has to learn. If the rollout did
   not complete, the remediation did not work, and the retry budget is
   exactly the mechanism for trying again.

5. **Verify is bounded by the step's own timeout, defaulting to 60s.**
   Executions are serialised: one attempt runs to completion inside a single
   reconcile, so a long verify holds the queue. Sixty seconds covers a
   rolling restart of a small Deployment and is short enough that a stuck
   verify is not mistaken for a stuck operator. The bound is a parameter so
   a strategy that knows its workload takes longer can say so.

6. **Events are addressed through the RESTMapper, not a hardcoded table.**
   The engine holds targets as `kind/namespace/name` strings, and an event
   needs a group, version and kind. Resolving through the manager's
   RESTMapper means every future action gets addressable events with no
   entry to add anywhere — and it fails soft: an event that cannot be
   addressed is logged and skipped, never a reason to fail a remediation
   that otherwise worked.

7. **`Remediating` is published before the step runs, not after.** The
   event that matters most is the one explaining a change while it is
   happening. A single event at the end would be missing during precisely
   the minute someone is looking at a workload wondering what is restarting
   it.

8. **`Kubectl` is a field, not generated prose.** Each action states the
   command a human would have typed, because only the action knows it. It is
   never executed, never parsed, and nothing depends on its content — it is
   there so a reviewer who has not read the source can tell what happened,
   which is the cheapest trust remedik can buy.

## Risks / Trade-offs

- **The `Action` interface changes shape.** Source-breaking for anything
  implementing it out of tree. There is nothing out of tree, the API group
  is `v1alpha1`, and the alternative is doing it after there is.
- **Verify makes some steps slower.** A restart that used to record success
  in milliseconds now takes as long as the rollout. That is the point: the
  old number was measuring the wrong thing. Executions are serialised, so
  the cost is real and is capped by the timeout.
- **Events are cluster noise.** Three events per remediation, on objects
  that are already being watched during an incident. Kubernetes aggregates
  repeated events, and the alternative — an audit trail nobody finds — is
  worse.

## Open Questions

None blocking. Whether `Verify` should also run before `Execute`, as a
precondition ("is this workload already healthy? then do nothing"), is a
real question and a different feature: it would let a strategy skip
unnecessary work. It needs its own outcome on the state machine — a step
that did nothing on purpose — and belongs with the change that introduces
`blastRadius`, where "should this run at all" is already the subject.
