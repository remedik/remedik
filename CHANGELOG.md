# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **Exactly one instance acts.** `replicas: 1` was in the chart and nothing
  enforced it: `kubectl scale deploy/remedik --replicas=2` succeeded, and the
  second instance served the same Service and reconciled the same resources.
  The guards keep their state in memory, so the two would each enforce a
  cooldown the other could not see — the alert storm remedik exists to absorb
  would have been amplified. Nothing said so anywhere either, so somebody
  scaling for availability was doing the reasonable thing.

  A lease in the operator's namespace settles it. More than one replica is
  now failover, never additional throughput.

  A standby keeps listening and answers `503` with `Retry-After`, because not
  listening at all is indistinguishable from remedik being down — the one
  thing a gateway must never be mistaken for. The sender retries and the
  Service routes it to the leader.

  The guards are replayed when the lease is taken, not when the process
  starts: a standby that loaded at boot and took over six hours later would
  enforce six-hour-old cooldowns, which is the same mistake through a side
  door. An instance that cannot replay them stops.

  Three things were got wrong on the way, each found by the end-to-end test
  and each recorded in the change's proposal. The first version left every
  HTTP server needing the lease, which is controller-runtime's default for a
  runnable that says nothing — so a standby had no listener and refused the
  connection instead of answering 503, defeating the whole point. Retrying the status write on conflict, which corrupts
  records — see the note below. And gating readiness on leadership, which
  makes a standby never ready, so `helm --wait` never finishes and the
  failover cannot be installed with ordinary tooling.

- **The gateway stops accepting the moment shutdown begins.** During a
  rolling update the outgoing pod is still an endpoint and still holds the
  lease, so an alert could land on a process about to be killed and have its
  remediation cut in half. The record was honest about it — `Running` can
  only mean the process died — but losing a remediation on every upgrade is
  not a good trade. It now refuses with `503` on SIGTERM while finishing what
  it already holds.

- **A namespaces page** (`/namespaces`). One row per namespace remedik has
  remediated in: its posture, its outcomes, how many of its failures nobody
  was told about, and when it was last active. Ordered by what needs
  attention rather than by name, because a list somebody has to read all of
  does not answer "where is this going badly".

  It is deliberately not called health. remedik knows the remediations it
  ran, not whether the workloads in a namespace are well.

  No new permission and no new read: the page is an arrangement of records
  the dashboard already listed.

### Fixed

- The dashboard's `mode-failed` badge rendered in the muted palette, so a
  count meant to stand out did not.

## [0.1.0-rc.3] - 2026-08-16

The first release that completed found two things the pipeline had never had
a chance to be wrong about, both of the same shape: a release candidate
presenting itself as the current version.

- The GitHub release was created with `prerelease: false`, so `v0.1.0-rc.2`
  showed as the latest release. Somebody following the README would install
  a release candidate believing it was stable.
- The image pushed `:latest` unconditionally, so `docker pull
  ghcr.io/remedik/remedik` would hand back the same candidate.

Both now key off the tag: a hyphen in a semantic version means prerelease,
and a prerelease moves neither `:latest` nor the release pointer. `rc.2` was
marked as a prerelease retroactively.

## [0.1.0-rc.2] - 2026-08-16

`rc.1` was cancelled: its image build passed twenty minutes and was still
running. The build stage carried no `--platform`, so buildx ran all of it
under QEMU for `linux/arm64` — `go build` emulated instruction by
instruction on an amd64 runner. Pinning the stage to the build platform and
passing the target to the compiler moves it back to native speed, which is
free for a `CGO_ENABLED=0` binary.

Nothing else changed. Everything below still applies.

## [0.1.0-rc.1] - 2026-08-16 [cancelled]

The first published artifact, and a release candidate for one reason: the
release pipeline itself had never run. Everything below is implemented and
verified — `make verify` for the unit suite, `make e2e` for the whole loop
on a real cluster, and CI green on the commit this was cut from — but a
multi-arch build, keyless signing, an SBOM attestation and a chart push to
OCI are four things that had only ever been read, never executed.

If this tag produces a signed image, a verifiable attestation and an
installable chart, the next one is v0.1.0.

Every OpenSpec change is archived, so `openspec/specs/` is the current
contract rather than a proposal.

### Added

- **Leader election** (`add-leader-election`). `replicas: 1` was in the chart
  and nothing enforced it: `kubectl scale --replicas=2` succeeded, and the
  second instance served the same Service and reconciled the same resources.
  The guards keep their state in memory, so the two would each enforce a
  cooldown the other could not see — the alert storm remedik exists to absorb
  would have been amplified. The requirement was not written down anywhere
  either, so somebody scaling for availability would have been doing the
  reasonable thing.

  Exactly one instance now holds a lease, reconciles, and accepts alerts. A
  standby keeps listening and answers 503 with a `Retry-After` rather than
  refusing the connection: a Service has one set of endpoints, so a replica
  with no listener is indistinguishable from remedik being down.
  Alertmanager retries a non-2xx, the Service routes it, and the alert
  lands. `replicaCount` is now a value, defaulting to one, and raising it is
  failover rather than a hazard.

  The guards are replayed when the lease is taken, not when the process
  starts — a standby that loaded at boot and took over six hours later would
  have enforced six-hour-old cooldowns, which is the same mistake through a
  side door. An instance that cannot replay them stops.

- **The dashboard holds up at any cluster size** (`scale-the-dashboard`).
  Measured on 150 namespaces, 40 strategies and 10,000 records — a mid-sized
  platform team, not an outlier — it rendered **190 filter links** and took
  **49.7 ms** to build one list page.

  The 49.7 ms was shape, not slowness: every filter option counted itself
  with its own pass over every record, so the cost was options × records and
  grew as a product. At 500 namespaces it would have been seconds, per page
  load, on the operator that is also running remediation. Counting each
  dimension in one pass gives identical numbers in **1.2 ms**. The slow
  version read as obviously correct; the benchmark is what made it obviously
  wrong, and it is now checked in so the claim stays a measurement.

  The controls now follow the cardinality. A handful of values stays links —
  one click, no menu. Above a threshold the dimension becomes a select
  carrying every value with its count, beside links for the busiest few:
  browsers give a select keyboard type-ahead, which is exactly the "find my
  namespace among 150" interaction, for no JavaScript. Its form sends the
  other clauses back as hidden fields, so choosing a namespace does not
  clear a state somebody already chose.

  The list pages rather than truncating, and paging composes with the
  filters. A page beyond the end is clamped, because history is pruned and a
  bookmarked page 40 may have become nothing.

  Offering a select was only safe once the refresh stopped replacing the
  controls: the list marks its rows and counts as the live region and keeps
  its controls outside it.

- **The dashboard is four pages, and the front one is a dashboard**
  (`rework-dashboard-pages`). The overview carried the stats, the dry-run
  report, the filters and a fifty-row table; it read as a list with
  decoration, so "is anything wrong right now?" was three scrolls down under
  a filter for a different question.

  `/` is now panels — posture, what needs attention, activity over the last
  day as bars, and where remediation is happening — each one a claim with a
  link to its evidence. The "needs attention" panel orders by how much
  silence each entry represents, so a failed escalation, which means nobody
  was told, is listed above a failure that was reported. Every figure links
  to the list that explains it.

  `/remediations` is the list, with the filters and the counts. A panel is
  one struct, one builder and one template block, and a page is a route, a
  view, a template and a nav entry — so "namespace health" is an addition
  rather than a rearrangement.

  No new dependency and no request leaving the cluster: the activity chart
  is bars in CSS from numbers the server already has, with the same numbers
  in a table underneath.

- **Per-namespace posture** (`add-namespace-posture`). `dryRun` was one flag
  on the operator, so a cluster was either all simulation or all action —
  which is not how anybody adopts a tool that holds write access. The real
  shape is live in `staging` and reporting in `prod` until the reports have
  earned the change, and that needed two installs.

  ```yaml
  dryRun: true              # report everywhere
  namespacePosture:
    staging: live           # ...except act in staging
  ```

  It works in both directions. The namespace consulted is the **workload's**,
  not remedik's, and a target with no namespace — a node, a webhook — takes
  the default, which ships as dry-run.

  Where the setting lives was the decision worth making. A label on the
  `Namespace` reads better and is wrong: remedik's RBAC is cluster-wide,
  granted once on the strength of a reviewed set of actions, so a namespace
  label would let anyone with `edit` there promote themselves from
  "reported" to "remediated" using permissions somebody else granted. On a
  strategy is no better, since a strategy spans namespaces. In the chart,
  posture and RBAC sit in the same file and disagree in a diff somebody
  reads.

  The posture is resolved once, when the record is created, and written onto
  it — like the steps and the retry budget, and for the same reason. The
  reconciler now obeys the record and no longer ORs in the operator's
  current flag, because the two legitimately disagree; that OR would have
  silently simulated a namespace somebody deliberately made live.

  The one real cost is somebody reading `dryRun: true` and believing nothing
  acts. No naming fixes that, so it is made hard to miss: the chart prints
  the overrides after every install, the operator warns at startup, the
  dashboard's badge reads `Mixed` and names the namespaces,
  `remedik_namespace_posture{namespace,posture}` makes it queryable, and
  every record carries the posture it ran under.

  To stop everything, scale the deployment to zero or disable the strategy.
  Both are instant; changing `dryRun` never was, because it needs a rollout
  either way.

- **Filtering on the dashboard** (`add-dashboard-filters`) by namespace,
  strategy and state. The filter is a query string, which is not a shortcut
  — the dashboard allowlists GET and HEAD before routing, so a filter
  needing server-side state could not exist here — and the consequence is
  the useful part: a narrowed view is a URL somebody can paste into an
  incident channel. The controls are a plain form, so they work with
  JavaScript off, and the auto-refresh preserves them.

  The counts follow the filter, because figures that disagreed with the rows
  beneath them would be worse than no filter. The controls' choices do not,
  because a control whose options shrink as you use it is one you can get
  stuck in. An unknown value renders "nothing happened there" rather than a
  400, and a namespace filter excludes cluster-scoped records, because a
  node is in no namespace.

  The controls render **above** `<main>`, outside the region the ten-second
  auto-refresh replaces. With them inside, a selection made and not yet
  applied was destroyed faster than anybody reaches Apply, and the filter
  appeared to do nothing — a failure invisible to every test that fetches
  the page and visible immediately to anybody using it. Moving the markup
  makes it impossible rather than something the JavaScript remembers not to
  do, and deleted the JavaScript first written to work around it. An active
  filter is now stated on the page as removable chips, one per clause.

  There is no cluster filter, deliberately: remedik watches the cluster it
  runs in, so the control would offer a choice of one. `clusterName` instead
  puts a name in the header and leads the browser title, which solves the
  real problem — three port-forwarded dashboards producing three
  identical-looking tabs.

- **Escalation when a remediation fails** (`add-failure-escalation`). The
  loop this project exists to serve had no end: a remediation that failed
  was recorded as `Failed` and nothing else happened, and nobody goes
  looking at 3am for a remediation they did not know was attempted.

  `onFailure.steps` is a second plan, run once the retries are spent, so
  "escalate" means whatever the cluster already uses — a `webhook.call` to
  PagerDuty, a `job.run` that hands the incident to a pipeline. It is
  deliberately not a notification subsystem: escalating is an action like
  any other, so it is gated by the same RBAC, audited in the same record,
  and there is nothing to configure separately.

  Four properties, each chosen against an obvious alternative:

  - **It cannot change the outcome.** A remediation that escalated is still
    a remediation that did not work, and a record turning green because
    somebody was paged would be the most misleading thing here.
  - **It runs once the retries are spent, not per attempt.** Paging on the
    first failure of three pages for something about to fix itself, and a
    page that is usually unnecessary is a page people learn to ignore.
  - **It runs during a dry run** — the only thing in remedik that does. A
    trial is exactly when an operator wants to see the escalation path work;
    the steps are told `remedik_dry_run="true"`, so nobody is paged for an
    incident that did not happen.
  - **It is not retried.** Looping on a failed page during an incident helps
    nobody. `status.escalation` records that it failed, and
    `remedik_escalations_total{outcome="Failed"}` is its own alertable
    signal: a remediation failed and nobody was told.

  The steps receive the alert's labels plus `remedik_remediation`,
  `remedik_strategy`, `remedik_target`, `remedik_reason`, `remedik_message`,
  `remedik_attempts` and `remedik_dry_run`, so a webhook body explains the
  incident with no templating. remedik's keys overwrite any alert label of
  the same name — an escalation that can be lied to by whoever writes the
  alerting rules is worse than no escalation.

  The dashboard shows it as its own section, and says so explicitly when a
  remediation failed with no escalation declared: "it failed and no alert
  went anywhere" is a fact worth stating rather than leaving to be inferred
  from an absence. The overview marks each row `paged` or `page failed`
  beside its state, because that is where somebody looks first and the
  second of those is the only thing on the page meaning nobody knows.

  `RemedikEscalationFailing` alerts on it, at **critical**: every other rule
  in the bundle can wait for somebody to look, and this is the rule about
  nobody looking. Grafana gains a "Did anybody find out?" row — escalations
  by outcome, and failures that declared no escalation at all, which is the
  number to check before concluding remedik is quiet because nothing is
  wrong.

- **`make e2e` now covers thirteen of the fourteen actions**, up from six.
  A rollback needs real revision history, a scale needs a real HPA to
  refuse, an expansion needs a real StorageClass, and a webhook needs
  something at the other end — none of which a unit test can supply. The
  endpoint is remedik's own gateway, so the whole outbound path (POST,
  bearer credential from a Secret, 2xx, and the 401 that fails a step
  honestly) is proven without the test needing anything outside the cluster.
  Eighty assertions, up from fifty-seven.

  The fourteenth is `pvc.expand` succeeding: kind's `standard` StorageClass
  does not allow expansion, so the e2e proves the refusal instead — which is
  the behaviour worth guaranteeing, since the API server would otherwise
  accept the patch and change nothing. `CONTRIBUTING.md` now states what
  kind cannot host and why, rather than leaving it as a gap.

- **Node actions and volume expansion** (`add-node-actions`), landing last
  on purpose — after the contract could verify its own work and after a
  guard existed that could bound them:

  - **`node.cordon`** and **`node.uncordon`** — the safest pair in the
    catalogue and the right first response to almost every node alert:
    nothing moves, nothing restarts, and one command undoes either. Two
    verbs, so a strategy can be granted "stop scheduling here" without being
    granted the ability to drain. Both are idempotent, because an alert
    fires repeatedly and a strategy that failed on the second firing would
    be unusable.
  - **`node.drain`** — cordons first (draining without it races the
    scheduler), then evicts every eligible pod through the Eviction API. It
    is the one place in remedik where a 429 is not an immediate failure: a
    drain is a loop, and a PodDisruptionBudget saying "not yet" is the
    normal answer partway through one, so refusals are retried until the
    step's timeout. **A drain that does not finish fails the step, and the
    node stays cordoned** — half-drained is the worst state to leave a node
    in, and reporting it as success would lose capacity no dashboard
    accounts for. DaemonSet pods, mirror pods and pods with no controller
    are skipped, with the record saying how many and why. This is the widest
    permission remedik holds, and the chart says so.
  - **`pvc.expand`** — grows a claim, but only where the StorageClass sets
    `allowVolumeExpansion`. Without that check the API server accepts the
    patch and nothing happens: remedik would record a success that did
    nothing, which is worse than failing because nobody goes looking. It
    never shrinks, and it says on the record that expansion is one-way.

- **Scaling and rollback** (`add-scaling-and-rollback`), the *careful* tier
  the `blastRadius` guard was built to bound:

  - **`deployment.rollback`** — what `kubectl rollout undo` does, and the
    highest-value action in the catalogue: the usual cause of a crash loop
    at 3am is the deploy at 2:50. It **refuses a workload Argo CD or Flux
    manages**, because those controllers revert a rollback within minutes —
    remedik would record a success while the outage continued, and the
    incident would spend its time discovering that two systems are fighting.
    The refusal names the controller and says to revert the commit instead.
  - **`deployment.scale`** — sets or increases replicas, and **refuses a
    Deployment a HorizontalPodAutoscaler targets**, because the autoscaler
    reverts it on its next interval. A relative increase must state a
    ceiling: "increase by" with no maximum is an alert storm with a credit
    card, and a default here would be a number invented for somebody else's
    budget. Verification waits for the replicas to become *available* —
    replicas that cannot schedule are not capacity.
  - **`hpa.scale`** — raises an autoscaler's `maxReplicas`, the one
    mechanical answer to `KubeHpaMaxedOut`. It never lowers one: reducing
    headroom during an incident is not a remediation.

  There is deliberately no scale-down verb. Every alert that reaches remedik
  says something is unhealthy, and "run less of it" is not an answer to that.

- **A third guard: `blastRadius`** (`add-blast-radius-guard`). The other two
  ask about time — how recently, how often. This one asks about state: how
  broken is this workload already? `minAvailable` refuses while it has that
  many available replicas or fewer ("never touch the last one");
  `maxUnavailablePercent` refuses while that share is already unavailable
  ("do not add to something already struggling").

  It is what bounds the actions that *remove* capacity rather than replacing
  it, and it lands **before** the node actions rather than after — shipping
  destructive verbs and bounding them later means every cluster that
  upgraded in between ran them unbounded.

  It is a second opinion beside a PodDisruptionBudget, not a substitute: the
  PDB was written by whoever owns the workload, this by whoever decided an
  automated system may act on it unattended. Most workloads have no PDB at
  all.

  **The guard fails closed.** If the workload cannot be read — no
  permission, an API error — it refuses, and the refusal names the missing
  permission on the strategy's events. A guard that permits an execution
  when it could not evaluate its own condition is not a guard, it is a
  comment. "Nothing to measure" is treated as a different answer: a node has
  no replica count, so the guard allows rather than paralysing the cluster.

- **Three escape hatches** (`add-escape-hatches`), because four built-in
  verbs will never cover what people need at 3am and "remedik cannot do X"
  is a reason not to install it at all:

  - **`webhook.call`** — POSTs the alert, the strategy and the plan to a URL,
    optionally authenticated from a Secret in remedik's own namespace. A
    response outside 2xx fails the step, with the body on the record: a
    pipeline that answered 500 did not run, and a Succeeded record beside it
    would be a lie the audit trail tells for ever. The credential never
    reaches the record or the recorded command.
  - **`job.run`** — runs an image as a Job **in remedik's own namespace**,
    under a ServiceAccount the step names. Never remedik's own, which is
    refused explicitly; the default is `default`, which can do nothing, so
    forgetting produces a Job that cannot act rather than one that can do
    everything the operator can. The command is a JSON array, so no quoting
    rules are invented. Verification waits for the Job and records its exit
    code and the tail of its output.
  - **`script.run`** — the same with the script mounted from a ConfigMap in
    remedik's namespace, so a runbook can be edited with kubectl. Read from
    that namespace only: anywhere else and anyone with write access to any
    namespace could have code executed by the operator.

- **`action.Request`** replaces the widening parameter list on `Plan`,
  `Execute` and `Verify`. It carries the alert's labels and the identity of
  the remediation and strategy, which a verb handing the incident to
  something outside the cluster cannot work without — a gap that was
  invisible while every action restarted something it had already resolved.

- **The chart ships the NetworkPolicy SECURITY.md promised.** It named
  NetworkPolicies as a v0.1.0 commitment and the chart created none, which
  is the kind of gap that costs more credibility than the feature was worth.
  It is ingress-only and opt-in, naming who may reach the gateway, metrics
  and the dashboard — and the chart *refuses to render* one with no peers
  for the gateway, because that policy would stop Alertmanager silently and
  silence is this project's worst failure mode. Egress is deliberately not
  restricted: remedik's one outbound call is to the API server, whose
  address belongs to the cluster rather than to a chart that would be
  guessing.

- **`priorityClassName`**, because a single-replica operator evicted under
  node pressure stops remediating and nothing reports that it has.

- **`make specs`** checks that the spec-first workflow was actually
  followed: every change carries its reasoning, every capability spec states
  requirements with scenarios and the word SHALL, nothing is archived with
  unfinished tasks, and every archived change's capability reached
  `openspec/specs/`. CONTRIBUTING.md made those claims; now something checks
  them. Dependency-free on purpose — a gate that only runs where somebody
  installed a tool is not a gate.

- **remedik can now be monitored** (`add-observability-bundle`). The metrics
  had existed since the MVP and nothing had ever scraped them:
  kube-prometheus-stack discovers `ServiceMonitor` resources, and the chart
  only ever created a Service, so a stock install collected exactly zero
  `remedik_*` series.

  - Four metrics describing the operator's **posture** rather than its
    throughput: `remedik_build_info`, `remedik_dry_run`,
    `remedik_strategies` by enabled state and `remedik_remediation_records`
    by state. Without them a flat remediation rate is unreadable — dry-run,
    no enabled strategies and a quiet week look identical. The two that
    depend on cluster state come from a collector reading the manager's
    cache when Prometheus scrapes, so they cost no API call and cannot go
    stale; a read that fails reports no series rather than zero, because
    zero enabled strategies is a real and alarming value.
  - An optional **`ServiceMonitor`** with configurable labels. The labels
    are load-bearing: the Prometheus Operator selects on them, and one
    created without the selector's label is created, ignored, and hard to
    notice.
  - An optional **`PrometheusRule`** with six alerts about remedik itself —
    down, ingest failing, nothing ever matching, most remediations failing,
    deliveries truncated, repeated unauthenticated attempts. Something
    holding write access to a cluster should be watched by the same
    monitoring it consumes.
  - A **Grafana dashboard**, versioned in the chart and optionally shipped
    as a ConfigMap. Every series colour is pinned by name, because Grafana
    assigns palette colours by series order: without pinning, filtering out
    a series repaints the survivors and "Failed" inherits the colour
    "Succeeded" had.

- **Three more actions** (`add-workload-actions`), all reversible and scoped
  to one object:

  - **`workload.restart`** — the rolling restart, for StatefulSets and
    DaemonSets as well as Deployments. It takes the kind from whichever of
    the `deployment`, `statefulset` or `daemonset` labels the alert carries,
    because in the kubernetes-mixin alerts the label naming the object also
    says what it is. `deployment.restart` stays exactly as it was, so
    existing strategies keep working and keep their narrower permission.
  - **`pod.delete`** — evicts one pod **through the Eviction API, never a
    delete**. Deleting a pod ignores PodDisruptionBudgets entirely; eviction
    is the only call that checks them, and a 429 is recorded as a refusal
    naming the budget, with the pod left running. The permission granted
    says the same thing: `create` on `pods/eviction`, never `delete` on
    `pods`. It also refuses a pod with no controller owner, because nothing
    would recreate it — that is deletion, not remediation — unless the step
    says `requireOwner: "false"`.
  - **`job.delete`** — removes a failed Job and its pods so the CronJob that
    owns it produces a clean run.

  All three verify their own work: the rollout completes, the pod is gone or
  replaced by one with a different UID, the Job no longer exists.

- **The chart's action permissions are now a table.**
  `charts/remedik/action-rbac.yaml` lists what each action may do and why;
  the ClusterRole grants an action's rules only when it is enabled. The same
  values decide which actions the operator registers, so a strategy naming a
  disabled action is reported as unusable when it is applied rather than
  failing during the incident it was written for. `make helm-lint` now
  checks that with every action off, nothing is granted on any workload, and
  that enabling one grants that one's rules.

- **A step now reports whether the remediation worked, not just that the API
  call was accepted** (`add-action-contract-v2`). Actions may implement an
  optional `Verify`: a read-only post-condition the engine calls after
  `Execute` and never in dry-run. `deployment.restart` uses it to wait for
  the rollout to reach the observed generation with every replica updated,
  available and ready. A rollout that does not finish inside the step's
  `verifyTimeout` fails the step, and the retry budget applies as it would
  to any other failure — because a restart that did not fix anything is not
  a success.

- **The object being remediated now explains itself.** Events are published
  on the workload — `Remediating` before a step, `Remediated` or
  `RemediationFailed` after — each naming the Remediation record and the
  strategy responsible. `kubectl describe deployment payments/api` answers
  "what restarted this?" without the reader having to know remedik exists.
  Targets are addressed through the manager's RESTMapper, so actions added
  later inherit this with nothing to register; an event that cannot be
  addressed is logged and skipped rather than failing a remediation that
  worked. No new RBAC: publishing events is a permission the operator
  already held.

- **Steps record the equivalent kubectl command and structured outputs.**
  `status.steps[].kubectl` carries the command a human would have typed —
  recorded, never executed — so a change is reviewable by someone who has
  never read remedik's source. `status.steps[].outputs` carries what the
  action specifically knew (replicas, restart timestamp, resource version),
  and `status.steps[].target` names the object each step acted on, which a
  multi-step plan needs and the single target on the spec cannot express.
  The dashboard shows all of it.

- **A read-only dashboard** (`add-readonly-gui`), served by the operator on
  its own port and **off by default**. Three pages: an overview with counts
  by outcome and the 50 most recent executions; one page per `Remediation`
  showing the triggering alert and its labels, the plan, each step's phase,
  message and timings, the attempt count and why it ended as it did; and the
  strategy list with matchers, guards, steps and last run, with disabled
  strategies visibly disabled.

  When dry-run is on, the overview leads with the report an operator shows
  their team: how many remediations would have run, over what period, across
  how many targets, broken down by strategy, with the exact plan line each
  one would have executed.

  Read-only is structural. The handler is built from a `client.Reader`, so
  it holds no method that writes; GET and HEAD are allowlisted before
  routing, so anything else is 405 on every path, including ones added
  later. It adds no RBAC — the pages read what the reconciler already
  watches, from the manager's cache — and `make helm-lint` fails if the
  rendered Role or ClusterRole differ by a byte between the dashboard being
  on and off.

  Pages and the stylesheet are `html/template` and `go:embed`: no npm, no
  bundler, no second release artifact, and no request to any host outside
  the cluster, which a test asserts against the embedded files and the
  rendered output. A content security policy of `default-src 'none'` says
  the same thing to the browser. The chart exposes a ClusterIP Service and
  no Ingress; the token is presented either as a bearer header or as the
  password in the browser's own prompt, because a browser cannot be told to
  send a bearer header.
- **The MVP loop works end to end.** An Alertmanager delivery reaches the
  gateway, a strategy matches it, guards decide, the engine executes and
  records the outcome as a `Remediation` resource — installable with
  `helm install`, verified by `make e2e` on a real kind cluster.
- Remediation controller (`add-mvp-core` tasks 3.2–3.6): the execution state
  machine, with retries and exponential backoff, `Interrupted` recovery
  after a crash, and pruning of terminal records per strategy. Guard history
  is rebuilt from existing resources at startup, so cooldowns and hourly
  counts survive a restart.
- Alert sink: matches alerts to strategies, evaluates guards and creates the
  execution record. The plan and retry budget are copied onto the record, so
  it still explains the run after the strategy is edited or deleted.
- Guard rejections are published as Kubernetes events on the strategy, so
  `kubectl describe remediationstrategy` answers "why did nothing happen?"
  without anyone having to find the operator's logs.
- Prometheus metrics on the manager's endpoint: alerts received, truncated
  and unmatched, ingest errors, guard rejections, remediations started and
  finished by outcome, and execution duration.
- Helm chart: Deployment, Services, ServiceAccount and RBAC assembled from
  the actions actually enabled, a token Secret, and install notes carrying
  the exact Alertmanager receiver snippet. Dry-run is the install default,
  and the chart refuses to render an unauthenticated gateway unless asked.
- Container image: distroless, non-root, read-only root filesystem.
- `make e2e`: an end-to-end test on a throwaway kind cluster asserting
  authentication, dry-run leaving the workload untouched, a real restart,
  the cooldown refusing a repeat, an unmatched alert being ignored, and
  guards surviving an operator restart. Plus `make docker-build` and
  `make dev-deploy`.

- `deployment.restart` action (`add-mvp-core` task 4.2): rolling restart via
  the same `kubectl.kubernetes.io/restartedAt` annotation `kubectl rollout
  restart` uses, so the Deployment controller honours maxUnavailable,
  readiness and PodDisruptionBudgets. Never deletes pods.
- Step execution and retry timing (`add-mvp-core` task 3.2, partial):
  `StepRunner` sequences a strategy's steps, stops at the first failure and
  records the rest as Skipped; `Backoff` gives deterministic exponential
  retry delays capped at ten minutes.
- Action contract and registry (`add-mvp-core` task 4.1): every remediation
  verb implements Resolve / Plan / Execute, so dry-run calls Plan only and
  the mutating path is never reached. The registry rejects duplicate and
  empty action names and reports unknown actions with the list of known
  ones.
- API types (`add-mvp-core` task 1.1): `RemediationStrategy` (cluster-scoped)
  and `Remediation` (namespaced audit record) in `remedik.dev/v1alpha1`,
  with validation markers, print columns and status subresources. The
  package depends on k8s.io/apimachinery alone — not controller-runtime —
  so it stays cheap for clients and tools to import.
  `make generate` and `make manifests` produce DeepCopy code and CRDs with a
  pinned controller-gen; `make verify-codegen` fails on stale output.
- First cookbook entry: `examples/strategies/pod-crashloop.yaml`.
- Strategy selection and guards (`add-mvp-core` task 3.1): label-equality
  matching with most-specific-wins and deterministic tie-breaking, plus
  cooldown and maxPerHour guards that report which guard rejected an
  execution and when to retry. Includes an in-memory execution history for
  the engine to keep hot.
- Alert gateway (`add-mvp-core` task 2): HTTP receiver for Alertmanager
  webhooks with constant-time bearer authentication, body-size limits and
  normalization of grouped deliveries into individual alert events. Served
  by the binary on `:8090` (`--gateway-bind-address`, `--gateway-path`,
  token from `REMEDIK_GATEWAY_TOKEN`); alerts are logged until the
  remediation engine lands.

- Project scaffolding: spec-driven process (OpenSpec), architecture decision
  records, security policy, contribution guide, CI pipeline, Helm chart
  skeleton, and a minimal binary serving health/readiness probes.
- OpenSpec change `add-mvp-core` specifying the MVP: alert gateway,
  `RemediationStrategy`/`Remediation` CRDs, deterministic execution engine
  with guards and dry-run, and the `deployment.restart` action.
- Dev tooling (`add-dev-tooling`): golangci-lint, yamllint/yamlfmt and
  helm-docs Make targets with pinned tool versions installed into
  `hack/bin/`; `make dev-up`/`dev-down` for a local kind cluster with
  kube-prometheus-stack; CI extended to run the full lint suite and check
  generated chart docs.
- `make versions`: reports every pinned version against the latest upstream
  release, so drift is visible without hunting through files.

### Known

- **~~A conflict on the terminal status write rewrites a success as
  interrupted.~~ Explained, and it was not a defect.**

  The conflict is the check. `Reconcile` reads through the manager's cache,
  which is eventually consistent, so a second reconcile — triggered by the
  first one's own status write — can read a copy that still says `Running`
  after the first has already recorded `Succeeded`. By this operator's rule,
  `Running` means the process died, so that stale read decides the
  remediation was interrupted. Its write carries the old `resourceVersion`
  and is refused; the work is requeued; the next pass reads a fresh copy,
  finds a terminal state and stops. Nothing is lost, and the refusal is
  guaranteed rather than lucky: the stale object's version is by definition
  older than the one just written.

  So the two reverted attempts to retry the write were removing the only
  thing protecting the record. The second one, which re-read from the API
  server and re-applied the status the reconciler had already built, let the
  false `Interrupted` overwrite a `Succeeded` — a remediation whose every
  step worked, recorded as failed.

  It was caught by re-running the reverted change against `make e2e` with
  the richer diagnostics: three log lines, twenty-four milliseconds apart,
  reading "remediation finished, state=Succeeded", then "remediation was
  interrupted", then "status write needed more than one attempt".

  `internal/engine/staleread_test.go` now holds it: a reconciler whose reads
  are behind its writes must not be able to overwrite a terminal record. The
  test was confirmed to fail when the retry is reintroduced, with the same
  `Failed/Interrupted` corruption the cluster produced.

  A retry here would only ever be safe if it re-decided after re-reading,
  rather than re-applying a decision made from data since overtaken. The
  separate, real hazard — two operator replicas each enforcing guards the
  other cannot see — is unaffected and still open, in
  `openspec/changes/add-leader-election`.

### Fixed

- **The first CI run on GitHub failed twice, for reasons worth keeping.**
  `verify` passed both times; `vuln` and `e2e` did not, which is why this was
  pushed before it was tagged.

  `go.mod` pinned `go 1.26.0` and CI installs exactly what `go.mod` says, so
  every job ran on a toolchain carrying eleven standard-library advisories.
  Nothing in this repository's own code was implicated. It was invisible
  locally because govulncheck ran only in CI and the local toolchain happened
  to be newer — a check passing by accident of the machine. `make verify`
  runs it now, and CI runs `make vuln` rather than installing
  `govulncheck@latest` inline, so there is one definition and a green run
  yesterday means the same thing today.

  `e2e` could not install kind: `/usr/local/bin` is root-owned on the hosted
  runner, so the download landed and the chmod was refused.

  Then a flake: the strategies page is served from the manager's cache, and a
  page can answer 200 before that cache holds what the page is about — most
  visibly just after a `helm upgrade` restarts the pod. The assertion now
  waits for the content, like the guard-event assertions already did, and
  says what it saw when it does fail.

- **The dashboard filter did not work, and neither of the first two fixes
  reached anybody.** The stylesheet, the script and the page shell live
  outside the content region, and the auto-refresh replaces only that
  region. A tab left open across an operator upgrade therefore keeps the old
  assets and the old markup for ever, refreshing its data through them —
  which is the most convincing way for a page to be wrong, and why "the
  filter still does not work" was the correct report each time.

  The page now carries its asset fingerprint, and the refresh reloads when
  the one it fetches differs. That is a defect in its own right: after any
  upgrade, an open dashboard was rendering new data through old markup.

  Filtering is now navigation. Every choice is a link, so there is no state
  between choosing and applying — nothing a refresh can destroy, no Apply
  button to reach before a timer fires, and no JavaScript on the path at
  all. Clicking the value in force removes it, so the same control narrows
  and widens, and each carries the count its choice would yield.

- **The list panicked on a filter that matched nothing**, returning an empty
  reply rather than a page. The display's "0 of 0" made the row loop start at
  index -1. Caught by `make e2e`, which is the only test here that fetches
  the page the way a browser does.

  A panic in a view builder now renders a 500 page saying what happened and
  that nothing in the cluster changed, instead of a closed connection — this
  is the page somebody opens when something is already wrong.

- **`/remediations` answered 307.** Only the trailing-slash form was
  registered, so the mux redirected every link on every page.

- **An action with no target no longer reports "on /".** `webhook.call`,
  `job.run` and `script.run` act outside the cluster and resolve to nothing,
  and every message about them carried a bare slash that read like a bug in
  remedik rather than a report of the one in the endpoint. A zero target now
  renders as nothing, which is already the record's convention for it.

- **The README said thirteen actions.** There are fourteen.

- **`make dev-deploy` left the old binary running.** The image tag comes
  from `git describe`, so an uncommitted change rebuilds the same tag with
  different contents and `helm upgrade` sees no diff to roll out. It now
  restarts the deployment, because a dev loop that silently deploys nothing
  costs more than the twenty seconds the restart takes.

- **`make dev-deploy` left the old CRDs in place.** Helm installs `crds/`
  once and never upgrades them, so every field added to `api/` was rejected
  with `unknown field` until somebody applied them by hand. The dev target
  now applies them server-side, and `charts/remedik/README.md` gives
  operators the three commands to do the same across a version upgrade —
  it previously said only that they had to.

- **`spec.dryRun` is written even when false.** It carried `omitempty`, so a
  live record simply had no such field and "was this one simulated?" was an
  inference from an absence — on the one record whose job is to explain
  itself, and now that posture varies by namespace. It stays optional in the
  schema: making it required would reject every record an earlier version
  wrote, on its next status update. `kubectl get remediations -o wide` shows
  it.

- **`make verify` now checks the chart README against its template.** The
  README is generated from `README.md.gotmpl`, and editing the generated
  file works until the next `make helm-docs` silently reverts it — which had
  already happened once here. CI has always caught this; `verify` calls
  itself "everything CI runs", so it catches it too. It compares
  regeneration against itself rather than against git, so it says the same
  thing in a clean checkout and in a dirty working tree.

- **The escalation's message no longer appears twice** on a remediation's
  page, once for the escalation and once for the single step that already
  said it.

- **Three flaky e2e assertions.** Guard refusals were checked after a fixed
  eight-second sleep, and events are written after the decision they
  describe, by however long the cluster feels like. One run in three failed
  on timing alone. They poll for the condition now — a suite people learn to
  re-run is worth less than no suite.

### Changed

- **The chart no longer ships values that do nothing.** `slack`,
  `escalation.pagerduty`, `audit.sinks`, `ai` and `packs` described features
  that are designed and not built. A key that quietly does nothing is worse
  than a missing one, because somebody sets it, believes it, and finds out
  during an incident. The designs stay in `docs/advanced-setup.md`, which
  says on its first line that none of it has shipped.
- Events are published through `k8s.io/client-go/tools/events` rather than
  the deprecated `record` API, which also gives each event an `action`
  field naming the verb that ran.
- Minimum Go version is now 1.26 (matches the Kubernetes ecosystem, which
  controller-runtime requires).
- Pinned previously floating versions: kube-prometheus-stack 88.3.0, kind
  v0.32.0 and helm v3.21.4 documented as prerequisites.
- CI actions updated: `actions/checkout` v4 → v7, `actions/setup-go`
  v5 → v7, `azure/setup-helm` v4 → v5.
