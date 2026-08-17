## Why

remedik will restart the same Deployment for ever.

`cooldown` spaces the attempts out. `maxPerHour` caps how many a strategy
starts. Neither ever concludes anything: after the hour, remedik tries again,
and again, for as long as the alert keeps arriving. A workload that breaks
every twenty minutes is restarted three times an hour, indefinitely, and
nobody is told — because from remedik's point of view every one of those
remediations *worked*.

That last part is the whole point and it is easy to miss. These are not
failures. The rollout completed, the pods came back ready, the record says
`Succeeded`. What is wrong is not the remediation; it is that remediation is
not the answer, and remedik is the only thing in the cluster in a position to
notice.

Two more things are wrong with what exists:

- **`maxPerHour` is counted per strategy, across every target.** One workload
  that breaks repeatedly consumes the whole budget, so a strategy protecting
  forty workloads stops protecting the other thirty-nine because of one.
- **A guard refusal tells nobody.** It produces an event and a metric, no
  `Remediation` record and no escalation. So the state remedik ends in —
  "I have stopped helping" — is the state with the least visibility.

## What Changes

- **A `giveUpAfter` guard**: after N remediations of the same target within a
  window, remedik stops remediating it and **escalates**, saying the thing that
  is true — repeated remediation is not fixing this, and it needs a person.
- **It is scoped to `(strategy, target)`**, so one broken workload cannot
  silence remediation for the others.
- **Giving up produces a `Remediation` record** with no steps, a state of
  `Failed`, and a reason of `GaveUp`. That is what makes it visible: it runs
  the strategy's existing `onFailure.steps`, appears in the list and on
  `/namespaces`, and is an audit entry rather than a log line.
- **One record per trip.** While the guard holds, further alerts are refused
  quietly; the page happens once, not on every delivery.
- **The record is excluded from guard history**, since remedik started nothing.

## Non-goals

- **Deciding whether the workload is healthy.** remedik knows it keeps being
  asked to remediate the same thing. It does not know why, and the record
  should not pretend to.
- **Un-tripping by hand.** The window slides: once the target has been quiet
  for the window, remediation resumes on its own. A latch somebody has to
  clear is a latch that stays set.
- **Replacing `maxPerHour`.** It still bounds a strategy's total rate, which is
  a different question from whether remediation is working for one target.

## Capabilities

### Modified Capabilities

- `remediation-strategies`

## Impact

- `internal/guards`: a third guard, still with no Kubernetes dependency.
- `internal/engine`: the sink creates the give-up record and escalates.
- `api/v1alpha1`: one guard field and one reason.
- No new RBAC.
