# remediation-execution Specification

## Purpose
Execute a selected strategy as an auditable state machine: one Remediation
resource per execution, sequential steps, retries with backoff, and a global
dry-run mode that simulates instead of acting.

## Requirements

### Requirement: Remediation record per execution

The engine SHALL create one `Remediation` resource (`remedik.dev/v1alpha1`)
for every accepted alert-strategy match, recording: the triggering alert
(fingerprint and labels), the strategy name, the resolved target, per-step
outcomes with timestamps, and a terminal state of `Succeeded`, `Failed`, or
`Simulated`.

The engine SHALL also publish Kubernetes events on the object being
remediated: one when a step begins, and one when it ends, naming the
remediation and the strategy responsible. Failing to publish an event SHALL
NOT fail the remediation.

#### Scenario: The remediated object explains itself

- **WHEN** remedik restarts a Deployment
- **THEN** `kubectl describe deployment <name>` shows events naming remedik, the strategy and the Remediation record, without the reader having to know remedik exists

#### Scenario: An unaddressable target does not break a remediation

- **WHEN** the target's kind cannot be resolved to a Kubernetes API kind
- **THEN** the events are skipped and logged, and the remediation proceeds

#### Scenario: Audit trail is queryable

- **WHEN** an execution finishes
- **THEN** `kubectl get remediations` shows its strategy, target, state and age, and the step-by-step record is present in the resource status

### Requirement: Sequential step execution with retries

The engine SHALL execute `spec.steps` strictly in order, stopping at the
first failed step; on failure it SHALL retry the strategy up to
`onFailure.retries` times with exponential backoff, and every attempt SHALL
be visible in the Remediation status.

Each recorded step SHALL carry, in addition to its phase and timings: the
object it acted on, the one-line summary of what was done or would be done,
the equivalent command a human would have typed, and any structured outputs
the action produced.

Actions SHALL receive, alongside the target and the step's parameters, the
triggering alert's labels and the identity of the remediation and strategy
responsible. An action that hands the incident to something outside the
cluster cannot do its job without them.

An action MAY implement a post-condition check. When it does, the engine
SHALL call it after the step executes, SHALL record its result on the step,
and SHALL treat a failed check as a failed step. The check SHALL NOT be
called in dry-run, where nothing was executed for it to verify.

#### Scenario: Retry then succeed

- **WHEN** step 1 fails on the first attempt, `retries` is 1, and the second attempt succeeds
- **THEN** the Remediation ends `Succeeded` and its status shows 2 attempts

#### Scenario: An action can name where the work came from

- **WHEN** a step hands the incident to something outside the cluster
- **THEN** it has the alert's labels and the names of the remediation and strategy to send with it

#### Scenario: A step says what a human would have typed

- **WHEN** a step completes
- **THEN** its record carries the equivalent kubectl command, so the change is reviewable by someone who has not read remedik's source

#### Scenario: A remediation that did not work is not recorded as success

- **WHEN** an action executes without error but its post-condition check fails
- **THEN** the step is Failed, the check's message is recorded, and the retry budget applies as it would to any other failure

#### Scenario: Dry-run does not verify

- **WHEN** the operator is in dry-run
- **THEN** no post-condition check runs, because nothing was executed

#### Scenario: An action that touches no object records no target

- **WHEN** a step acts on nothing in the cluster
- **THEN** its record carries no target, rather than a placeholder nobody can look up

### Requirement: Posture

Dry-run SHALL be structural: actions implement `Resolve`, `Plan` and
`Execute` separately, and a simulated step calls `Plan`, so the mutating path
is never reached. A step's outcome in dry-run SHALL be `Simulated`, and the
remediation's terminal state SHALL be `Simulated` when every step was.

The posture SHALL be per namespace. The operator takes a default and a set
of overrides keyed by namespace, each `live` or `dryRun`; the posture for a
remediation is the override for the namespace of the object it targets, or
the default when there is none. This is what lets one install act where
remediation has been earned and report everywhere else.

The namespace consulted SHALL be the **target's**, not the operator's own. A
target with no namespace — a node, a webhook, a Job run outside any
workload — SHALL take the default.

The posture SHALL be resolved once, when the `Remediation` is created, and
recorded on it. The reconciler SHALL use the recorded posture and SHALL NOT
consult the operator's current default, because the two can legitimately
disagree and re-reading would silently simulate a namespace somebody
deliberately made live.

An override naming a namespace that does not exist SHALL NOT be an error:
remedik does not watch namespaces, and one can be created after the install.

#### Scenario: One install, two postures

- **WHEN** the default is dry-run and `staging` is overridden to `live`
- **THEN** a remediation targeting `staging` executes and one targeting any other namespace is simulated

#### Scenario: A namespace held back from a live cluster

- **WHEN** the default is live and `prod` is overridden to `dryRun`
- **THEN** a remediation targeting `prod` is simulated and the rest execute

#### Scenario: The target's namespace decides, not remedik's

- **WHEN** remedik runs in `remedik` and that namespace is overridden to `live`, while the default is dry-run
- **THEN** a remediation targeting `payments` is still simulated

#### Scenario: A cluster-scoped target takes the default

- **WHEN** a strategy drains a node, which has no namespace
- **THEN** the default posture applies

#### Scenario: An execution keeps the posture it started with

- **WHEN** a remediation is created live and the operator's default is changed before its retry runs
- **THEN** the retry runs under the posture recorded on the resource

### Requirement: The posture is visible without reading the values file

The operator SHALL log the resolved posture at startup, and SHALL warn when
it is mixed, naming the namespaces that differ.

The chart SHALL print the overrides after an install or upgrade.

The operator SHALL expose `remedik_dry_run` for the default and
`remedik_namespace_posture{namespace,posture}` for each override.

The dashboard SHALL show `Mixed` rather than the default's badge whenever
any namespace differs, and SHALL name those namespaces. Every `Remediation`
already records the posture it ran under.

#### Scenario: The default does not describe the cluster

- **WHEN** the default is dry-run and one namespace is live
- **THEN** the dashboard's badge reads `Mixed` and names that namespace, rather than reading `Dry-run`

#### Scenario: A typo is visible where somebody looks

- **WHEN** an override names a namespace that does not exist
- **THEN** the operator starts, logs the posture it was given, and the dashboard shows the same list

### Requirement: Crash-safe state

The engine SHALL persist state transitions in the Remediation resource such
that an operator restart mid-execution results in the in-flight execution
being marked `Failed` with reason `Interrupted`, rather than any mutating
step being silently re-run.

#### Scenario: Restart mid-execution

- **WHEN** the operator restarts after step 1 of 2 has completed
- **THEN** the Remediation is marked `Failed` with reason `Interrupted` and no step executes twice without an explicit retry

### Requirement: Bounded history

The engine SHALL prune terminal Remediation resources beyond a configurable
retention (default: most recent 200 per strategy) so that alert storms cannot
grow storage without bound.

#### Scenario: Old records pruned

- **WHEN** a strategy accumulates more terminal Remediations than the retention limit
- **THEN** the oldest terminal records are deleted and the newest are kept

### Requirement: Escalation when a remediation fails for good

The engine SHALL run `spec.onFailure.steps` after a remediation has failed and
its retry budget is exhausted, and SHALL NOT run them between retries. The
plan SHALL be copied onto the `Remediation` at creation time, like `steps` and
`retries`, so an execution already under way keeps the behaviour it started
with.

Escalation steps are ordinary actions: they are resolved through the same
registry, gated by the same RBAC, and recorded with the same per-step detail.

The engine SHALL record the outcome in `status.escalation`, separate from
`status.steps`, with a phase of `Succeeded` or `Failed`, the per-step record,
and a message when it failed.

#### Scenario: The retry budget is spent before anybody is paged

- **WHEN** a strategy allows two retries and every attempt fails
- **THEN** the remediation runs three times and the escalation runs exactly once, after the third

#### Scenario: A successful remediation escalates nothing

- **WHEN** the steps succeed
- **THEN** `status.escalation` is absent and no escalation step runs

#### Scenario: The escalation is kept apart from the remediation's own steps

- **WHEN** an escalation has run
- **THEN** `status.steps` contains only the remediation's steps, and the escalation's appear under `status.escalation.steps`

### Requirement: Escalation cannot change the outcome

A remediation that escalated SHALL remain `Failed`. The engine SHALL NOT
change `status.reason` or `status.message` to describe the escalation, and a
failed escalation SHALL NOT produce a reconcile error, a requeue, or a further
attempt at the remediation.

The engine SHALL bound the escalation with its own deadline, so an
unreachable endpoint cannot hold the execution open indefinitely.

#### Scenario: A page that could not be sent is recorded, not retried

- **WHEN** the escalation's webhook returns 503
- **THEN** the remediation stays `Failed` with its own reason, `status.escalation.phase` is `Failed` with the endpoint's error, and nothing is retried

#### Scenario: Escalating is not succeeding

- **WHEN** a remediation fails and its escalation succeeds
- **THEN** the terminal state is `Failed`

### Requirement: Escalation runs during a dry run

The engine SHALL execute escalation steps for real even when the remediation
was simulated, and SHALL NOT call their `Plan` path. This is the only
exception to dry-run in remedik, and exists so a trial proves the escalation
path before it is needed.

The engine SHALL tell the escalation which it was, by setting these labels on
the context it hands the steps, overwriting any alert label of the same name:

| Label | Value |
| --- | --- |
| `remedik_remediation` | the record's name |
| `remedik_strategy` | the strategy that matched |
| `remedik_target` | the object, as `kind/namespace/name` |
| `remedik_reason` | the machine-readable cause |
| `remedik_message` | the human-readable detail |
| `remedik_attempts` | how many attempts were made |
| `remedik_dry_run` | `"true"` when nothing was actually changed |

#### Scenario: A trial proves the escalation path

- **WHEN** the operator is in dry-run and a simulated remediation fails
- **THEN** the escalation steps are executed rather than planned, and are told `remedik_dry_run="true"`

#### Scenario: An alert cannot lie to the escalation

- **WHEN** the triggering alert carries a label named `remedik_reason`
- **THEN** the escalation receives remedik's value, not the alert's

### Requirement: Escalation is visible without reading YAML

The dashboard SHALL show the escalation as its own section of a remediation's
page, stating whether anybody was told before listing which steps ran. A
remediation that failed with no escalation declared SHALL say so explicitly
rather than showing nothing.

The operator SHALL expose `remedik_escalations_total{strategy,outcome}`.

#### Scenario: The silent failure is named

- **WHEN** a remediation failed and its strategy declares no `onFailure.steps`
- **THEN** its page says no alert went anywhere, and names the field that would change that

#### Scenario: A failed page does not look calm

- **WHEN** an escalation failed
- **THEN** the page states that nobody may know, in the same tone it uses for a failed remediation

### Requirement: Exactly one instance acts

The operator SHALL hold a lease in its own namespace, and only the instance
holding it SHALL reconcile `Remediation` resources or accept alerts.

The guards keep their state in memory, so two instances would each enforce a
cooldown the other cannot see — the alert storm remedik exists to absorb
would be amplified instead. A replica count above one SHALL therefore be
failover and never additional throughput.

The gateway SHALL keep listening on every replica and answer `503` with a
`Retry-After` when this instance does not hold the lease, rather than
refusing the connection. A Service has one set of endpoints, so a replica
with no listener is indistinguishable from remedik being down — which is
the one thing a gateway must never be mistaken for. Alertmanager retries a
non-2xx and the Service routes the retry, so the alert lands.

Authentication SHALL be checked before leadership, so an unauthenticated
sender cannot learn which replica holds the lease.

#### Scenario: Scaling up does not double the remediation

- **WHEN** the deployment is scaled to two replicas
- **THEN** one lease exists, one replica answers alerts, and the other answers 503 and records nothing

#### Scenario: A standby is not silent

- **WHEN** an alert reaches the replica without the lease
- **THEN** it answers 503 with Retry-After rather than closing the connection, and nothing is recorded

### Requirement: The guards are warmed when the lease is taken

The in-memory guard state SHALL be rebuilt from the existing `Remediation`
resources at the moment this instance becomes the leader, not when the
process starts, and the gateway SHALL NOT accept alerts until that has
completed.

A standby that loaded at boot and took over hours later would enforce
hours-old cooldowns, which is the mistake leader election exists to prevent
arriving through a side door.

An instance that cannot rebuild its guards SHALL stop rather than remediate
without them.

#### Scenario: A late handover does not remediate on stale state

- **WHEN** a standby becomes the leader long after it started
- **THEN** it replays the guard history before accepting anything

#### Scenario: A failed replay is not survivable

- **WHEN** the guard history cannot be read
- **THEN** the operator stops rather than accepting alerts without guards

### Requirement: Readiness is not leadership

The readiness probe SHALL report ready on every replica that is running,
including a standby that holds no lease.

Gating readiness on leadership was tried and rejected: a standby then never
becomes ready, so `helm --wait` and `kubectl rollout status` never complete
on a deployment with more than one replica — the failover this change exists
to allow could not be installed with ordinary tooling.

A standby is doing its job. It waits, and it answers `503` with
`Retry-After` so the sender retries onto the leader. That is where the
contract is enforced, and readiness is not.

A consequence, which callers must expect: a ready replica is not proof that
alerts are being accepted. The leader accepts only once it holds the lease
and has replayed the guards.

#### Scenario: A standby is ready

- **WHEN** a replica is running without the lease
- **THEN** its readiness probe reports ready, and it answers alerts with 503

#### Scenario: More than one replica can be installed

- **WHEN** the chart is installed with two replicas and `--wait`
- **THEN** the install completes
