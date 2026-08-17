## Why

The escalation stops at its first failed step. So a fallback page cannot be
configured, which is the one thing a paging path exists to have.

```yaml
onFailure:
  steps:
    - action: webhook.call     # Alertmanager
    - action: webhook.call     # PagerDuty, if Alertmanager could not be reached
```

If Alertmanager is unreachable, the second step is recorded `Skipped` and
nobody is paged. The configuration reads as a fallback and behaves as a single
point of failure.

Stopping at the first failure is right for a remediation plan: step two acts
on the result of step one, so running it after a failure would act on a state
that never happened. It is exactly wrong for an escalation, where the steps
are not a sequence but **alternative ways to reach a person**, and the whole
point is that one of them works.

This is the most consequential reliability defect in the escalation path,
because it is invisible until the day it matters: every step succeeds in
testing, so the skipped-step behaviour is never seen.

## What Changes

- **A failed escalation step no longer skips the rest.** Every step runs.
- **The escalation is `Failed` only when every step failed.** One channel
  getting through means somebody was told, which is the fact the record is
  reporting.
- **`onFailure.mode`** for the case where running everything is wrong:
  `all` (the default, today's behaviour when steps succeed) runs every step;
  `firstSuccess` stops at the first one that gets through, so an ordered
  fallback chain does not page twice.
- **A `Partial` phase is not added.** The record already lists every step with
  its own phase; a fourth summary value would be a second way to say the same
  thing, and the question the summary answers is "was anybody told".

## Non-goals

- **Retrying a step.** Still deliberately absent: looping on a page during an
  incident helps nobody, and a second channel is a better answer than the same
  channel again. `mode: firstSuccess` with the same endpoint twice is available
  to anybody who disagrees.
- **Ordering guarantees between channels.** Steps run in order, as they always
  have. Nothing here makes them concurrent.

## Capabilities

### Modified Capabilities

- `remediation-execution`

## Impact

- `api/v1alpha1`: one enum field on `OnFailure`, and on the `Remediation`
  record so an escalation runs under the mode it was created with.
- `internal/engine`: the escalation runner stops sharing the remediation
  plan's stop-on-failure rule.
- No RBAC change, no new reach.
