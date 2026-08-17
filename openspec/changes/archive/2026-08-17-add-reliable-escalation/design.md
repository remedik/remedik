## Context

`StepRunner.Run` stops executing after a step fails and marks the remainder
`Skipped`. Both the remediation plan and the escalation use it.

For the plan that is correct and load-bearing: step two of "scale up, then
restart" must not run if the scale failed.

For the escalation it inverts the intent. The steps are channels.

## Decisions

### 1. The runner keeps its rule; the escalation stops using it

Two callers with opposite requirements should not share one flag threaded
through the runner — the runner's rule is a property of a plan, and the
escalation is not a plan.

So `Run` is unchanged, and the escalation iterates its own steps. The
duplication is about ten lines and it makes the difference legible: somebody
reading `escalation.go` sees that every channel is tried, without holding the
runner's semantics in their head.

### 2. `Succeeded` means somebody was told

The record's job is to answer "was anybody told". If one channel got through,
the answer is yes, and the failed channel is still visible as its own step
with its own message.

The alternative — `Failed` if any step failed — would make the loud alert
`RemedikEscalationFailing` fire on a night when the page landed, and an alert
that cries wolf about paging is worse than none.

### 3. The default is `all`, not `firstSuccess`

`all` is what a working configuration does today: when every step succeeds,
every step runs. Choosing `firstSuccess` as the default would silently stop
running step two for everybody who wanted a page *and* a ticket.

So `all` preserves every configuration that works today, and the only change
in behaviour is to the case that is broken: a failed step no longer silences
the ones after it.

`firstSuccess` exists because an ordered fallback under `all` pages twice
whenever both channels work, and people who are paged twice remove the
fallback — which loses the reliability the mode was added for.

### 4. The mode is copied onto the record

Like `dryRun`, the mode is resolved when the record is created and written
onto it. An escalation must run under the policy that was in force when the
remediation started, not whichever policy a later edit installed; and the
record must say which, or the audit trail cannot be read back.

### 5. Escalation steps must stay side-effect free, and this raises the stakes

`all` means a step that failed halfway no longer prevents the next one. Since
escalation steps run during a dry run too, the existing rule — put nothing
here that changes the cluster — matters more, not less. The field
documentation says so where somebody writing a strategy will read it.

## Risks / Trade-offs

- **`all` with an ordered fallback pages twice.** Named, documented, and
  `firstSuccess` is one line.
- **A failing channel is now tried on every escalation** rather than
  short-circuiting the rest. Each step has its own bounded timeout and the
  escalation as a whole has `EscalationTimeout`, so the ceiling is unchanged.
