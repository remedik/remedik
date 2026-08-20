<p align="center">
  <img src="docs/brand/remedik-banner.png" alt="remedik — Kubernetes auto-remediation operator" width="640">
</p>

# remedik

> Predictably boring auto-remediation for Kubernetes alerts.

[![CI](https://github.com/remedik/remedik/actions/workflows/ci.yml/badge.svg)](https://github.com/remedik/remedik/actions/workflows/ci.yml)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/remedik/remedik/badge)](https://scorecard.dev/viewer/?uri=github.com/remedik/remedik)
[![Go Reference](https://pkg.go.dev/badge/github.com/remedik/remedik.svg)](https://pkg.go.dev/github.com/remedik/remedik)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

**It is 03:00 and `payments-api` is crash-looping again.** Somebody is
paged, opens a runbook, reads three lines, types `kubectl rollout restart`,
and goes back to bed. The same three lines, the fortieth time this quarter.

That work is mechanical, and mechanical work is exactly what should not need
a person at 03:00. What has kept it manual is not difficulty — it is that
handing a robot write access to production is a decision nobody wants to
sign off on.

remedik is built for that signature. It turns Alertmanager alerts into
remediation you can audit, bound and switch off, and it is deliberately
boring: strategies are custom resources you keep in git, every execution is
recorded as a `Remediation` object in the cluster, guards bound the blast
radius before anything runs, and an LLM never sits in the execution path.

### The painpoints this was built for

Every row is something that happens on a real on-call rotation, and the
mechanism next to it is the answer this project actually ships — not a plan.

| On call, this happens | What remedik does about it |
| --- | --- |
| **The same runbook, the fortieth time.** Three lines of `kubectl`, at 03:00, for a failure mode everybody already understands. | The three lines become a `RemediationStrategy` in git. The alert triggers it; nobody is woken for the ones it handles. |
| **Nobody signs off on automation with write access.** So the runbook stays manual, and everybody agrees it should be automated one day. | Dry-run is the install *default*, and per namespace. It records what it would have done, for as long as you want, before it is allowed to do anything. RBAC is generated only for the actions you enable — turn everything off and it can only read. |
| **The alert storm.** One bad deploy, forty alerts, and the automation makes it worse than the outage. | Guards run before anything: `cooldown` per target, `maxPerHour` per strategy, and `blastRadius`, which refuses to touch the last healthy replica. A guard that cannot evaluate its own condition **refuses**. |
| **Automation that hides the problem.** It restarts the thing every twenty minutes for three weeks, and nobody ever learns that the app leaks memory. | The `giveUpAfter` guard stops after N remediations in a window, says why, and escalates instead. The dashboard counts what it gave up on. |
| **Remediation failed and nobody was paged.** The worst outcome of automating anything: the alert was swallowed by the thing that was supposed to fix it. | `onFailure.steps` is a second plan that runs when the retries are spent — and it runs during a dry run too, so the paging path is proved before it is needed. `remedik_escalations_total{outcome="Failed"}` is its own alert: it failed *and* telling you failed. |
| **Some things must not happen unattended.** Draining the node the database is pinned to is not a pod restart. | `execution.mode: approval`. The remediation waits in `AwaitingApproval`, nothing is even planned, and a `kubectl patch` decides it — attributable in your cluster's audit log. Silence escalates after a timeout, because the failure mode of a human gate is that nobody looks. |
| **You cannot stop it mid-incident.** The one thing you want at 04:00 is a switch, and it needs to work without a rollout. | `kubectl patch configmap remedik-pause` forces dry-run on every replica within seconds. remedik's RBAC on that object is read-only: **it cannot un-pause itself**. Records keep appearing, marked as suppressed, so you can see what you stopped. |
| **The post-incident question: what did the automation actually do?** Logs have rotated, the strategy has been edited since, and nobody can reconstruct it. | Every run — including simulations — is a `Remediation` object holding the triggering alert, its own copy of the plan, each step's outcome and timings, and the `kubectl` a human would have typed. |
| **"It worked in staging."** Adoption is all-or-nothing, so it never starts. | `namespacePosture` overrides the default per namespace, in one install. Act in `staging`, report in `prod`, and move one namespace at a time. |

### Why you can actually run it

|  |  |
| --- | --- |
| **It does nothing by default** | Dry-run is the install default, and it is structural rather than a flag — actions implement `Plan` and `Execute` as separate methods, so a simulated run never reaches the mutating one. Read the reports for a week before changing anything. |
| **Adoption is not all-or-nothing** | Posture is per namespace. Act in `staging`, report only in `prod`, one install. |
| **Permissions follow features** | The chart grants a permission only because a named action needs it. Enable nothing, and remedik can read. `make verify` fails if that stops being true. |
| **Every action explains itself** | Each `Remediation` records the plan, the `kubectl` a human would have typed, what changed, and whether it worked — so an incident review reads the record, not the logs. |
| **When it fails, somebody is told** | `onFailure.steps` is a second plan. If that fails too, the record says so, because "we tried to tell you and could not" is the thing you need to find later. |
| **The supply chain is checkable** | Multi-arch images signed with cosign keyless, an SBOM attested to the image, every GitHub Action pinned to a commit SHA, and `govulncheck` in the gate. |

Nothing is published yet: `v0.1.0` is the first release meant to be
installed, and its packages stay private until this repository is. Until
then, `./hack/try.sh` runs the whole loop from a checkout, on a throwaway
cluster — see below.

```console
# Verify a release before you trust it — no key required.
cosign verify ghcr.io/remedik/remedik:v0.1.0 \
  --certificate-identity-regexp '^https://github.com/remedik/remedik/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

### What it looks like

The dashboard is off by default and read-only by construction — built from a
`client.Reader`, and answering anything but GET and HEAD with 405 before
routing. It answers the two questions that are painful through kubectl:
*is anything wrong right now*, and *what did this one actually do*.

<p align="center">
  <img src="docs/screenshots/overview.png" alt="The overview: posture, what needs attention, activity over the last day, and where remediation is happening" width="820">
</p>

With fifty namespaces, "what happened" is the wrong question. `/namespaces`
answers where it is going badly, putting the failures nobody was told about
at the top:

<p align="center">
  <img src="docs/screenshots/namespaces.png" alt="The namespaces page: one row per namespace, with its posture, outcomes, and the failures nobody was told about" width="820">
</p>

Every failure has a page that explains itself — the plan, the error, what that
error actually means, and whether anybody was told. The explanation is a rule
over fields the record carries, and it names them: an explanation nobody can
check is an opinion, which is also why there is no language model anywhere near
it.

<p align="center">
  <img src="docs/screenshots/detail.png" alt="A failed remediation: the step that failed, and an escalation that also failed" width="820">
</p>

When a strategy asks before it acts, `/approvals` is the queue — ordered by how
soon each one expires, because this is the only state with a clock on it, and
carrying what approving it would run. The dashboard still cannot write
anything: it prints the command that decides it.

<p align="center">
  <img src="docs/screenshots/approvals.png" alt="The approvals queue: what is waiting, how long is left on each, what approving it would run, and the commands that decide it" width="820">
</p>

The [remediations list](docs/screenshots/remediations.png), a
[single waiting record](docs/screenshots/approval.png) and
[strategies](docs/screenshots/strategies.png) are in
[docs/screenshots](docs/screenshots), all regenerated by
[`hack/screenshots.sh`](hack/screenshots.sh) — against a real cluster, or
against `make dev-dashboard`, which serves every page with a cluster's worth of
made-up history and no cluster at all.

**Status: alpha.** The core loop works end to end — alert in, guarded
remediation out, audit trail in the cluster — covered by unit tests and an
end-to-end test that builds a throwaway cluster and runs the whole loop
against real objects. The API may still change; every change is specified in
[`openspec/`](openspec/) before it lands.

## What it does today

```yaml
apiVersion: remedik.dev/v1alpha1
kind: RemediationStrategy
metadata:
  name: pod-crashloop
spec:
  trigger:
    match:
      alertname: KubePodCrashLooping
  guards:
    cooldown: 15m        # do not restart the same workload again this soon
    maxPerHour: 4        # an alert storm cannot amplify into an outage
  steps:
    - action: deployment.restart
  onFailure:
    retries: 1
    steps:                        # and if that still does not work, page somebody
      - action: webhook.call
        with:
          url: https://events.pagerduty.com/v2/enqueue
          secretRef: pagerduty-routing-key
          secretKey: key
```

```console
$ kubectl get remediations -n remedik
NAME                  STRATEGY        TARGET                    STATE       AGE
pod-crashloop-x7k2q   pod-crashloop   deployment/payments/api   Succeeded   2m
pod-crashloop-b91mm   pod-crashloop   deployment/checkout/web   Simulated   1h
```

**Fourteen actions ship today**, each a separate permission the chart grants
only when you enable it, and only `deployment.restart` on by default:

| | Action | What it does |
| --- | --- | --- |
| **Workloads** | `deployment.restart` | Rolling restart of a Deployment |
| | `workload.restart` | The same, for StatefulSets and DaemonSets too |
| | `pod.delete` | Evicts one pod **through the Eviction API**, so a PodDisruptionBudget can refuse |
| | `job.delete` | Deletes a failed Job so its CronJob makes a clean run |
| **Capacity** | `deployment.rollback` | Puts the previous revision back — refuses under Argo CD or Flux |
| | `deployment.scale` | Sets or increases replicas — refuses when an HPA owns the workload |
| | `hpa.scale` | Raises an autoscaler's ceiling; never lowers it |
| **Nodes** | `node.cordon` / `node.uncordon` | Stop and resume scheduling. Reversible, moves nothing |
| | `node.drain` | Cordon, then evict everything, honouring disruption budgets |
| | `pvc.expand` | Grows a volume — only where the StorageClass allows it |
| **Anything else** | `webhook.call` | POSTs the incident to your pipeline |
| | `job.run` / `script.run` | Runs your container or your runbook script as a Job |

Several of those descriptions say "refuses", and that is the interesting
part. Deleting a pod ignores disruption budgets entirely; eviction is the
only call that checks them, so remedik holds `create` on `pods/eviction` and
never `delete` on `pods` — it *cannot* force a pod out. A rollback in a
GitOps cluster would be reverted within minutes, so it refuses rather than
recording a success while the outage continues. A volume whose StorageClass
forbids expansion accepts the patch and does nothing, so remedik checks
first. And a remediation Job runs as a ServiceAccount you name, never
remedik's own, which is refused.

**A strategy says whether remedik can run it**, seconds after you apply it
rather than during the incident it was written for:

```console
$ kubectl get remediationstrategies
NAME            ENABLED   READY   MODE   RUNS   LAST RUN   AGE
pod-crashloop             True    auto   12     4m         21d
drain-safely              False   auto   0                 2d
```

`False` means a step names an action this build does not have — a typo, or one
of the thirteen the chart does not enable by default — and the message names the
step and lists what *is* enabled, so the two are told apart. It reports rather
than gates: nothing is suppressed because a status is stale.

**When remediation does not work, somebody gets told.** `onFailure.steps` is
a second plan that runs once the retries are spent, so the loop closes:

```
alert --> remedik --> remediate --> it worked, done
                              \--> it did not --> page whoever is on call
```

Escalating is not a notification setting — it is made of the same actions,
under the same RBAC, in the same audit trail. It never turns a failure into
a success, it is never retried during an incident, and it runs during a dry
run too — the one exception in remedik, so a trial proves the path before
anybody needs it. `remedik_escalations_total{outcome="Failed"}` is its own
alertable signal: a remediation failed and nobody was told.

**Some things should wait for a person, and remedik will.** A strategy can ask
before it acts, per strategy — a node drain is not a pod restart:

```yaml
execution:
  mode: approval          # auto (default), approval, or manual
  approvalTimeout: 30m
```

The remediation then sits in `AwaitingApproval` and does nothing — nothing is
resolved, nothing is planned — until somebody decides:

```bash
kubectl -n remedik patch remediation drain-safely-x7k2q --type merge \
  -p '{"spec":{"approval":{"decision":"approve","by":"dana"}}}'
```

A patch, not a bot: attributable in your cluster's audit log, and usable from a
terminal, a runbook, a GitOps commit or a chat integration you write. It runs
against the cluster as it is when you approve, not as it was when the alert
fired. And **silence escalates** — no decision within `approvalTimeout` and the
alert reaches on-call the ordinary way, because a gate that quietly drops what
nobody looked at is worse than no gate.

**And one command stops everything, with no restart:**

```bash
kubectl -n remedik patch configmap remedik-pause --type merge \
  -p '{"data":{"paused":"true","reason":"network incident"}}'
```

Every replica is dry-run within seconds. It does not go quiet: records keep
appearing, marked `Simulated` and labelled with your reason, so you can see what
was suppressed rather than guess. remedik's RBAC on that ConfigMap is read-only
on that one name — **it cannot un-pause itself**, because a switch the tool can
flip is not a switch.

**Posture is per namespace, so adoption is not all-or-nothing.** `dryRun` is
the default; `namespacePosture` overrides it for the namespaces that have
earned it, in one install:

```yaml
dryRun: true              # report everywhere
namespacePosture:
  staging: live           # ...except act in staging
```

It works in the other direction too — live by default, `prod: dryRun` — and
the namespace consulted is the *workload's*, not remedik's. The posture is
resolved once when the record is created and written onto it, so every
`Remediation` says which posture it ran under and an in-flight execution
keeps the one it started with.

There is also a read-only dashboard, off by default. Its front page answers
"is anything wrong right now?" — posture, what needs attention, activity
over the last day, and where remediation is happening — and each panel links
into the list behind it. `/remediations` is that list, filtered by
namespace, strategy or state. Every filter control is a link, so a narrowed
view is a URL you can paste to whoever is on call, and it works with
JavaScript off. `/namespaces` answers the question a platform team with
fifty of them actually has — where is this going badly — with one row per
namespace, ordered by failures nobody was told about rather than by name.

## See it work before you trust it

One command, on your laptop, with nothing simulated: a throwaway cluster, a real
Prometheus, a workload that really does crash-loop, a real alert through a real
Alertmanager, and remedik recording what it would do about it.

```bash
git clone https://github.com/remedik/remedik.git && cd remedik
./hack/try.sh
```

Needs Docker, [kind](https://kind.sigs.k8s.io/), kubectl and helm, and about
ten minutes — most of it spent waiting for Prometheus to decide the workload is
genuinely broken, which is the same wait a real cluster has.

It uses the same chart and the same values a real install uses. At the end it
prints where to look, the one flag that lets remedik act, and the command that
stops it again. `./hack/try.sh --clean` deletes the cluster. **Your own kubectl
context is never touched** — the demo keeps its own kubeconfig.

Then, when you want the same thing against your own cluster:
**[QUICKSTART.md](QUICKSTART.md)**. The step that catches people is pointing
Alertmanager at the gateway, so it is two `kubectl` commands there rather than
an edit to your monitoring stack.

<details>
<summary>The other two ways in, for contributors</summary>

```bash
make e2e           # the end-to-end suite: throwaway cluster, 166 assertions, then cleanup
make dev-up        # a cluster to keep: kind + Prometheus, Alertmanager and Grafana
make dev-deploy    # build, load and install remedik (dry-run on)
make dev-seed      # 150 namespaces of history, so the pages have something to say
```

`make e2e` is the honest demo of the *guarantees*: an unauthenticated delivery
is refused; a dry run records a plan without touching anything; turning dry-run
off actually restarts the Deployment, and the record confirms the rollout rather
than the patch; the cooldown refuses an immediate repeat and survives an
operator restart; an unmatched alert is accepted and ignored; a pod nothing owns
is refused; approval waits for a person and escalates when nobody looks; the
kill switch works with no restart; and an upgrade whose CRDs the cluster does
not have is refused instead of silently dropping fields.

</details>

## Where it fits, and where it does not

remedik is the decision and audit layer between an alert and an action. It
is not trying to replace the tools below — several of them it calls.

| If you have | remedik is | |
| --- | --- | --- |
| **Liveness probes, `restartPolicy`** | complementary | Kubernetes already restarts a process that died. It cannot know that the deploy at 02:50 is why the pod has been dying since 02:51. |
| **Argo Rollouts, Flagger** | complementary | They own the rollout they started and can undo it from their own analysis. remedik reacts to any alert, on any workload, including ones deployed before it existed — and refuses to roll back anything a GitOps controller manages, because two systems fighting is worse than neither acting. |
| **HPA, KEDA** | complementary | Those scale on a metric. remedik raises the ceiling when the autoscaler is pinned at its maximum and still losing, which is a different event. |
| **cluster-api `MachineHealthCheck`, medik8s** | defers to them | Node replacement is a cloud API with different credentials and a different blast radius. remedik drains the node and hands the replacement to whatever owns it. |
| **Runbooks, Ansible, an internal pipeline** | in front of them | The matching, the guards, the cooldowns and the audit trail are the parts nobody enjoys writing twice. `webhook.call` and `job.run` hand the work to what you already have. |
| **A commercial incident platform** | smaller, and yours | No agent, no SaaS, no data leaving the cluster. One operator, a Helm chart, and CRDs you keep in git. |

**When remedik is the wrong answer:** if the fix needs judgement, it needs a
person. remedik is for the incidents whose runbook is three lines you have
typed forty times, not for the ones where you have to think. The `blastRadius`
guard, the cooldowns and the per-namespace posture exist because the second
kind is always hiding among the first.

## Design pillars

**The execution path is deterministic.** YAML decides, guards bound, and
humans approve what you decide needs approving. Optional AI features read and
explain; they never execute. See
[ADR-0003](docs/adr/0003-deterministic-core-ai-read-only.md).

**Dry-run is a guarantee, not a flag.** Every action implements `Resolve`,
`Plan` and `Execute` separately. In dry-run the engine calls `Plan`, so the
mutating code path is never reached — a `Simulated` record cannot have
touched the cluster even if an action is buggy.

**Minimal trust.** One agent per cluster, RBAC generated only for the
actions you enable, distroless non-root image, no external orchestrator and
no database. Turning off `deployment.restart` removes its permission to
patch Deployments — and `make helm-lint` checks that with every action off,
nothing at all is granted on any workload.

**Guards bound the damage.** `cooldown` and `maxPerHour` ask about time;
`blastRadius` asks about state — never the last available replica, never a
workload already too degraded to touch. It **fails closed**: a guard that
permits an execution when it could not evaluate its own condition is not a
guard.

**The audit trail is a first-class object.** Every run — including dry-run
simulations — is a `Remediation` resource carrying the triggering alert, the
plan, per-step outcomes and timings. It keeps its own copy of the plan, so
the record still explains the run after the strategy is edited or deleted.

## Documentation

| Doc | Purpose |
| --- | --- |
| [QUICKSTART.md](QUICKSTART.md) | Install it, or work on it |
| [docs/invariants.md](docs/invariants.md) | **What remedik promises never to do** — read this before granting it write access |
| [docs/routing.md](docs/routing.md) | Waking on-call only when remediation did not work — the routing, and the safety net that makes it safe to rely on |
| [docs/troubleshooting.md](docs/troubleshooting.md) | **Why did nothing happen?** — the six gates an alert passes, and the command that answers each |
| [docs/architecture.md](docs/architecture.md) | Components, state machine, guards, topologies |
| [docs/managed-kubernetes.md](docs/managed-kubernetes.md) | EKS, GKE and AKS — what is the same, what differs about alerts and nodes, and what is untested |
| [docs/advanced-setup.md](docs/advanced-setup.md) | Hub/spoke, cloud packs, audit sinks, AI diagnosis (planned) |
| [charts/remedik/README.md](charts/remedik/README.md) | Every chart value |
| [examples/strategies/](examples/strategies/) | Cookbook |
| [docs/adr/](docs/adr/) | Why things are the way they are |
| [SECURITY.md](SECURITY.md) | Security policy and commitments |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Spec-first workflow |
| [CLAUDE.md](CLAUDE.md) | Orientation for AI assistants working on the repo |

## Roadmap

**What decides the order:** what people who run this ask for. Everything below
is either shipped or has an argument written down for why it is not built yet —
`openspec/changes/` holds the proposals, and anything not under `archive/` is
proposed rather than built.

### v0.1.0 — shipped

The whole loop, and the parts that make it safe to leave running.

- **Alerts in, decisions out.** Alertmanager gateway with bearer auth, strategy
  matching by label equality, `RemediationStrategy` and `Remediation` CRDs.
- **Fourteen actions** across workloads, capacity and nodes, plus the escape
  hatches (`webhook.call`, `job.run`, `script.run`). Each is a permission the
  chart grants only when you enable it.
- **Four guards**: `cooldown`, `maxPerHour`, `blastRadius`, and `giveUpAfter` —
  which stops remediating what is not getting better and escalates instead.
- **Dry-run as a structural guarantee**, per namespace, so adoption moves one
  namespace at a time.
- **Human approval** (`execution.mode: approval`) decided with `kubectl patch`,
  and `manual` for strategies that must never start from an alert.
- **A kill switch** that forces dry-run on every replica in seconds, with no
  restart, and that remedik cannot switch off itself.
- **Escalation** through `onFailure.steps`, which runs during a dry run too, so
  the paging path is proved before it is needed.
- **A read-only dashboard**, six pages, off by default and unable to write by
  construction.
- **Strategies that report on themselves** — a `Ready` condition naming the step
  that cannot run, and how often the strategy has fired, in `kubectl get`.
- **Retention** on a schedule that never deletes a record a guard still relies
  on, and leader election so two replicas are failover rather than double
  remediation.
- **Prometheus metrics** with a Grafana dashboard and alert rules, signed
  multi-arch images, an attested SBOM, and every Action pinned to a SHA.

### v0.2.0 — next

- **A Slack front end for the approval gate that already exists.** Approve and
  deny from a card instead of a terminal. Deliberately in this order: the gate
  is a Kubernetes write, so the bot is a convenience rather than the feature.
- **`notify.level`** per strategy (`none` / `onCompletion` / `verbose`), for
  teams that want to hear about successes too.
- **Audit sinks** — Splunk HEC, Loki, Elasticsearch — for the shops where the
  `Remediation` objects are not the system of record.

### After that

Ordered by how often it is asked for, not by how interesting it is.

- **`node.replace`.** Draining a node in a managed pool leaves it cordoned and
  empty until something replaces the instance, and remedik deliberately holds no
  cloud credentials. This is the one real gap on EKS, GKE and AKS —
  [docs/managed-kubernetes.md](docs/managed-kubernetes.md) says so plainly.
- **`ActionPlugin`.** `job.run` already runs any image as any ServiceAccount, so
  a custom action needs no code today. What is missing is a *typed* one, with its
  own parameters, validation and declared RBAC. Worth designing after seeing what
  people actually write, because a plugin mechanism inside something holding
  cluster write access is a trust surface.
- **Hub/spoke multi-cluster**, and with it the cluster filter the dashboard does
  not have. Today's operator sees one cluster because it runs in one.
- **An MCP server**, read-only, so an assistant can answer "what has remedik
  done in the last hour" without being anywhere near the execution path.
- **Continuous capability checks with SLI/SLO output** — a workload that keeps
  exercising what a cluster can actually do. Effectively a second product, and it
  deserves its own argument about what it measures.

### Not planned, on purpose

- **An LLM anywhere in the execution path.** AI-backed features read and
  explain; what runs is a strategy somebody declared, through guards somebody
  configured. See [ADR-0003](docs/adr/0003-deterministic-core-ai-read-only.md).
- **A write button on the dashboard**, until there is an identity model that can
  say *who* clicked it. An audit trail that cannot answer that is worse than a
  terminal.
- **Anything that resumes a half-finished mutating step.** A crash is failed as
  `Interrupted` and left for a person, because silently repeating an action is
  the worse outcome.

## Supporting this project

remedik is Apache-2.0 and stays that way: nothing is held back for a paid
tier, there is no telemetry, and no feature is behind a licence key.

If it saves your team a 03:00 page, [what sponsorship pays
for](docs/funding.md) is written down — cluster time for the end-to-end
suite, coverage across Kubernetes versions, and an external security review
of the code that holds write access to your cluster.

If money is not the right thing, telling us it runs in production is worth
more than most pull requests: it is what makes the next person's security
review shorter.

## License

[Apache-2.0](LICENSE)
