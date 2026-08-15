# Cookbook

Each recipe is a `RemediationStrategy` you can apply as-is, with the
reasoning behind its guards written down. Copy one, change the matchers to
suit your alerts, and start with the operator in dry-run.

| Recipe | Alert | What it does |
| --- | --- | --- |
| [pod-crashloop.yaml](pod-crashloop.yaml) | `KubePodCrashLooping` | Rolling restart of the Deployment |
| [oom-restart.yaml](oom-restart.yaml) | `KubeContainerOOMKilled` | Restart, cautiously and rarely |
| [scoped-to-one-namespace.yaml](scoped-to-one-namespace.yaml) | `KubePodCrashLooping` in one namespace | Narrower rule that wins over a broad one |

## Choosing guards

**`cooldown`** answers "how long before trying the same thing on the same
target again?". Set it longer than the time it takes to know whether the
remediation worked — a rollout plus a couple of scrape intervals. Too short
and a flapping alert becomes a restart loop.

**`maxPerHour`** answers "how bad can an alert storm get?". Think about how
many of these you would be willing to see happen unattended in an hour. It
is the difference between one noisy workload and a cluster-wide event.

**`retries`** is for transient failures — a conflict, a momentarily
unavailable API server. It is not for "it did not fix the problem": if the
first restart did not help, a second one rarely does, and a human should
look.

## Which strategy wins

When several strategies match an alert, the most specific one runs — the one
with the most matchers. Ties are broken by name, so the outcome never
depends on the order you applied them. That is what makes
`scoped-to-one-namespace.yaml` a safe override of a broad default.
