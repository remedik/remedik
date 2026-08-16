## Context

remedik can act, and it can hand work to something outside the cluster. What
it cannot do is say "that did not work" to a person. The gap is one field
wide, and the shape of the fix matters more than its size.

## Decisions

1. **Escalation is a plan, not a notification setting.** The alternative was
   `onFailure.notify.slack` and a growing set of channels to configure.
   remedik already has verbs that reach outside the cluster; pointing one at
   PagerDuty is the whole feature. It also means escalation is audited,
   guarded and dry-runnable exactly like everything else, rather than being
   a side channel with its own rules.

2. **The terminal state stays `Failed`.** A remediation that escalated is
   still a remediation that did not work. A record turning green because
   somebody was paged would be the single most misleading thing this project
   could do.

3. **Escalation is recorded separately from the remediation's steps.** "The
   restart failed" and "the page failed" are different facts with different
   responses, and folding them into one list would make the second look like
   a fourth attempt at the first.

4. **Escalation runs after retries are exhausted, not after each attempt.**
   Paging on the first failure of three would page for something that is
   about to fix itself, and a page that is usually unnecessary is a page
   people learn to ignore.

5. **Escalation runs in dry-run too, and it is the only thing that does.**
   This breaks a rule the project holds firmly, so it is worth stating why:
   dry-run exists to answer "what would happen?", and for a trial run the
   escalation path is the part most worth seeing work before it is needed.
   The steps are ordinary actions, so a `webhook.call` to a test endpoint
   changes nothing in the cluster — and what remedik *sends* says the run
   was simulated, so nobody is paged for an incident that did not happen.
   An escalation plan that used a mutating action would be a mistake, and
   the documentation says so plainly.

6. **A failed escalation does not change the outcome.** The remediation had
   already failed; there is no worse state to move to. It is recorded, and
   the dashboard shows it, because "we tried to tell you and could not" is
   exactly the thing somebody needs to find later.

7. **Guard rejections do not escalate.** A guard refusing is the system
   working as designed. Paging for it would teach people that remedik's
   pages are noise, which costs more than the one incident where it might
   have helped.

## Risks / Trade-offs

- **Escalation running in dry-run will surprise somebody** who put a
  mutating action in an escalation plan. That is a real risk of a real
  exception, mitigated by documentation and by the fact that a plan whose
  job is "tell somebody" has no reason to contain a restart. It is worth the
  surprise: a dry-run trial where the escalation path is untested is a trial
  that proved half of what it should.
- **Two plans in one resource** is more for a reader to hold. They are
  rendered separately, and only when an escalation exists.

## Open Questions

Whether an escalation should also fire when a remediation is *interrupted*
by an operator restart is worth deciding with the person who runs it: the
remediation did not complete, but nothing failed either. It is left out for
now, and the record still shows `Interrupted`.
