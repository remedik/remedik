# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

Everything below is implemented and verified: `make verify` for the unit
suite, `make e2e` for the whole loop on a real cluster. Every OpenSpec
change is archived, so `openspec/specs/` is the current contract rather than
a proposal.

### Added

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
  from an absence.

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

### Fixed

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

- **The escalation's message no longer appears twice** on a remediation's
  page, once for the escalation and once for the single step that already
  said it.

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
