# Supporting remedik

remedik is Apache-2.0 and will stay that way. Nothing in it is held back
for a paid tier, there is no telemetry, and no feature is gated behind a
licence key. If you never pay anything, you get the same software.

This page is here because "Support this project" with no explanation
converts badly, and for infrastructure software it reads as unserious.
Below is what the money is actually for.

## What it pays for

**Cluster time.** The end-to-end test builds a throwaway Kubernetes cluster
and runs the whole loop against real objects — every action, every guard,
the drain classification, the escalation path. It is the test that has
caught the defects unit tests could not: an RBAC rule that silently rejected
every event, a page that panicked on an empty filter, a release build that
was emulating arm64 instruction by instruction. Running it more often, on
more Kubernetes versions, costs money.

**Kubernetes version coverage.** Kubernetes ships three minor versions a
year and each one deprecates something. Testing against the supported range
rather than whichever version happened to be current is ongoing work, not a
one-off.

**Security review.** This is software that holds write access to a cluster.
An external review of the action contract, the RBAC assembly and the escape
hatches — `webhook.call`, `job.run`, `script.run` — is worth more than any
feature on the roadmap.

**Time.** The unglamorous half: answering issues, reviewing contributions,
writing the reasoning down, and saying no to features that would make the
tool less predictable.

## What it does not pay for

Priority support, a private fork, or a feature because somebody paid for it.
If a change is right for the project it lands whether or not money moved; if
it is wrong, it does not. Sponsorship buys the project's continued
existence, not a queue position.

## If your company depends on this

The most valuable thing a company can do is not always money.

- **Tell us it is running.** A single line — "we run this on N clusters" —
  is what makes the next person's security review shorter.
- **Report the near-misses.** A guard that refused when it should not have,
  or allowed when it should not have, is worth more than a feature request.
- **Contribute a strategy.** The cookbook is the fastest path from install
  to value, and the recipes that matter are the ones somebody actually runs.
- **Send the security review back.** If your team assessed this and found
  something, we would rather hear it than not.

## How

The sponsor button on the repository, once it is enabled — see
[`.github/FUNDING.yml`](../.github/FUNDING.yml). For an invoice, or anything
a card cannot do, open a
[private advisory](https://github.com/remedik/remedik/security/advisories/new),
which is the one channel here that is not public.
