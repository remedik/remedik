## Context

Guards answer "may this run?" and their answers are all about pacing:
`cooldown` is "not yet", `maxPerHour` is "not this many", `blastRadius` is
"not safely". None of them can say "not ever, and here is why".

## Decisions

### 1. It counts remediations, not failures

The state worth detecting is a workload that keeps needing remediation, and
those remediations are usually **succeeding**. The restart works; the problem
comes back. Counting failures would miss exactly the case this exists for.

So the guard counts completions for the target, whatever they concluded.

### 2. Scoped to (strategy, target)

"The same alert, in the same namespace, on the same cluster" is what an
operator means, and `(strategy, target)` is how remedik already spells it: the
target carries kind, namespace and name, and the strategy is which alert
matched. The cluster is implicit while remedik watches one.

The alert fingerprint was the other candidate and is wrong: a crash-looping pod
is recreated under a new name, so its fingerprint changes and the count would
reset every time — resetting precisely when the evidence is strongest.

### 3. "Consecutive" is a window, because remedik cannot see the alert stop

An operator says "five times in a row". remedik has no way to observe a streak
being broken: it never learns that an alert resolved, only that one arrived.

A window is the honest form of the same idea. Five restarts of one Deployment
in two hours means restarting is not the fix. Five over three months is a
Tuesday. The window is what carries that difference, so it is configured
explicitly rather than derived from `cooldown`, which would hide it.

### 4. Giving up creates a record, and that is the design

Every other guard refuses into silence: an event, a metric, no `Remediation`.
That is defensible for "not yet" and indefensible for "I have stopped".

So giving up produces a record with no steps, `Failed`, reason `GaveUp`. It
costs one object and buys everything: the strategy's own `onFailure.steps`
runs, so the page goes wherever that operator's pages already go; it is on the
list, on `/namespaces`, and in the audit trail; and the message says the true
thing, which is that remediation is not working here.

A record for something remedik declined to do is a fair objection. The answer
is that remedik did do something — it decided to stop, which is a decision an
operator needs to be able to find later.

### 5. That record must not feed the guards

It started nothing, so it must not count as a start, a completion, or a
cooldown. Otherwise the give-up record would itself extend the window that
produced it, and replaying history at startup would rebuild guard state from
decisions rather than actions.

The `GaveUp` reason is what the history loader and the recorder filter on.

### 6. One record per trip

While the guard holds, alerts keep arriving — Alertmanager repeats. A record
and a page for each would turn "stop paging about this" into a source of
pages.

So a trip is recorded once: if a `GaveUp` record for this target already exists
inside the window, further alerts are refused the ordinary quiet way.

### 7. It clears itself

Once the target has been quiet for the window, the count falls and remediation
resumes. Nothing to reset.

The consequence is bounded flapping — up to N remediations and one page per
window, for as long as the workload stays broken. That is the correct amount
of noise: it is proportional to the problem still existing, and it stops the
moment somebody fixes it.

A latch that a human has to clear was the alternative. It fails the other way:
somebody fixes the app, remediation never resumes, and the tool silently does
nothing until a person remembers a state they cannot see.

## Risks / Trade-offs

- **A workload that legitimately needs frequent remediation** — a memory leak
  restarted hourly on purpose — will trip this. That is arguably correct, and
  the answer is to set the count and window to match the intent, or leave the
  guard off, which is the default.
- **The window is a judgement call.** No default is right for every workload,
  so the guard is off unless configured, like `blastRadius`.
